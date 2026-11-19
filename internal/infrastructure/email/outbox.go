package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxTicketEmailAttempts = 5

// TicketEmailJob contains identifiers only. The recipient and signed QR are
// loaded from the paid order at send time and are never stored in the outbox.
type TicketEmailJob struct {
	ID            int64
	OrderID       int64
	AttemptID     uuid.UUID
	AttemptNumber int
}

type TicketEmailOutbox struct {
	pool *pgxpool.Pool
}

func NewTicketEmailOutbox(pool *pgxpool.Pool) *TicketEmailOutbox {
	return &TicketEmailOutbox{pool: pool}
}

// EnqueueTx must be called before the paid-order transaction commits. The
// unique order_id constraint makes webhook retries safe.
func (o *TicketEmailOutbox) EnqueueTx(ctx context.Context, tx pgx.Tx, orderID int64) error {
	if o == nil || orderID <= 0 {
		return errors.New("ticket email outbox is not configured")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO notifications.ticket_email_jobs(order_id, next_attempt_at)
		VALUES ($1, NOW()) ON CONFLICT (order_id) DO NOTHING
	`, orderID)
	if err != nil {
		return fmt.Errorf("enqueue ticket email: %w", err)
	}
	return nil
}

// ClaimNext serializes workers through SKIP LOCKED. A timed-out lease may be
// retried, so transport delivery is at least once if a process crashes after
// SMTP accepts a message but before the database acknowledges it.
func (o *TicketEmailOutbox) ClaimNext(ctx context.Context) (*TicketEmailJob, error) {
	if o == nil || o.pool == nil {
		return nil, errors.New("ticket email outbox is not configured")
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin ticket email claim: %w", err)
	}
	defer tx.Rollback(ctx)
	job := &TicketEmailJob{}
	err = tx.QueryRow(ctx, `
		SELECT id, order_id, attempts
		FROM notifications.ticket_email_jobs
		WHERE attempts < $1 AND (
			(status IN ('pending','retrying') AND next_attempt_at <= NOW())
			OR (status = 'sending' AND lease_until < NOW())
		)
		ORDER BY COALESCE(next_attempt_at, lease_until), id
		LIMIT 1 FOR UPDATE SKIP LOCKED
	`, maxTicketEmailAttempts).Scan(&job.ID, &job.OrderID, &job.AttemptNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select ticket email claim: %w", err)
	}
	job.AttemptNumber++
	job.AttemptID = uuid.New()
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET status='sending', attempts=$2, current_attempt_id=$3,
		    lease_until=NOW() + INTERVAL '2 minutes', next_attempt_at=NULL,
		    updated_at=NOW()
		WHERE id=$1
	`, job.ID, job.AttemptNumber, job.AttemptID.String())
	if err != nil {
		return nil, fmt.Errorf("update ticket email claim: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO notifications.ticket_email_attempts(id, job_id, attempt_number)
		VALUES ($1, $2, $3)
	`, job.AttemptID.String(), job.ID, job.AttemptNumber)
	if err != nil {
		return nil, fmt.Errorf("insert ticket email attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit ticket email claim: %w", err)
	}
	return job, nil
}

func (o *TicketEmailOutbox) MarkAccepted(ctx context.Context, job *TicketEmailJob, messageID string) error {
	if o == nil || job == nil {
		return errors.New("ticket email attempt is missing")
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE notifications.ticket_email_attempts
		SET status=CASE WHEN status='sending' THEN 'accepted' ELSE status END,
		    accepted_at=COALESCE(accepted_at, NOW()),
		    ses_message_id=COALESCE(NULLIF(ses_message_id,''),NULLIF($3,''))
		WHERE id=$1 AND job_id=$2
		  AND ($3='' OR ses_message_id IS NULL OR ses_message_id='' OR ses_message_id=$3)
	`, job.AttemptID.String(), job.ID, messageID)
	if err != nil {
		return fmt.Errorf("mark email attempt accepted: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("email attempt acceptance does not match SES message")
	}
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET status='accepted', lease_until=NULL, updated_at=NOW()
		WHERE id=$1 AND current_attempt_id=$2 AND status='sending'
	`, job.ID, job.AttemptID.String())
	if err != nil {
		return fmt.Errorf("mark email job accepted: %w", err)
	}
	return tx.Commit(ctx)
}

// MarkFailure records only a classification, never the provider response, which
// could contain a recipient address or other private information.
func (o *TicketEmailOutbox) MarkFailure(ctx context.Context, job *TicketEmailJob, code string) error {
	if o == nil || job == nil {
		return errors.New("ticket email attempt is missing")
	}
	if code == "" {
		code = "transport_error"
	}
	if len(code) > 64 {
		code = "transport_error"
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_attempts
		SET status='transport_failed', error_code=$3
		WHERE id=$1 AND job_id=$2 AND status='sending'
	`, job.AttemptID.String(), job.ID, code)
	if err != nil {
		return fmt.Errorf("mark email attempt failed: %w", err)
	}
	status := "retrying"
	var next *time.Time
	if job.AttemptNumber >= maxTicketEmailAttempts {
		status = "exhausted"
	} else {
		// One, two, four, then eight minutes. The bound avoids retry storms.
		at := time.Now().UTC().Add(time.Minute * time.Duration(1<<(job.AttemptNumber-1)))
		next = &at
	}
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET status=$3, next_attempt_at=$4, lease_until=NULL, updated_at=NOW()
		WHERE id=$1 AND current_attempt_id=$2 AND status='sending'
	`, job.ID, job.AttemptID.String(), status, next)
	if err != nil {
		return fmt.Errorf("schedule email retry: %w", err)
	}
	return tx.Commit(ctx)
}

func (o *TicketEmailOutbox) MarkRejected(ctx context.Context, job *TicketEmailJob, code string) error {
	if o == nil || job == nil {
		return errors.New("ticket email attempt is missing")
	}
	if code == "" || len(code) > 64 {
		code = "invalid_order_data"
	}
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_attempts
		SET status='rejected', error_code=$3
		WHERE id=$1 AND job_id=$2 AND status='sending'
	`, job.AttemptID.String(), job.ID, code)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET status='rejected', lease_until=NULL, next_attempt_at=NULL, updated_at=NOW()
		WHERE id=$1 AND current_attempt_id=$2 AND status='sending'
	`, job.ID, job.AttemptID.String())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (o *TicketEmailOutbox) ExhaustExpiredFinalLeases(ctx context.Context) error {
	if o == nil || o.pool == nil {
		return errors.New("ticket email outbox is not configured")
	}
	_, err := o.pool.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET status='exhausted', lease_until=NULL, updated_at=NOW()
		WHERE status='sending' AND lease_until < NOW() AND attempts >= $1
	`, maxTicketEmailAttempts)
	return err
}

func recipientHash(address string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address))))
	return hex.EncodeToString(sum[:])
}

func (o *TicketEmailOutbox) IsSuppressed(ctx context.Context, address string) (bool, error) {
	var exists bool
	err := o.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM notifications.email_suppressions WHERE recipient_sha256=$1)
	`, recipientHash(address)).Scan(&exists)
	return exists, err
}
