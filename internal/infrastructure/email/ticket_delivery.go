package email

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/qr"
)

type TicketEmailSender interface {
	SendOrderTicketsEmailWithAttempt(string, string, string, []TicketEmailItem, uuid.UUID) error
}

var ErrTicketEmailNotEligible = errors.New("paid order is not eligible for ticket email")

type TicketEmailDispatcher struct {
	pool   *pgxpool.Pool
	outbox *TicketEmailOutbox
	sender TicketEmailSender
}

func NewTicketEmailDispatcher(pool *pgxpool.Pool, sender TicketEmailSender) *TicketEmailDispatcher {
	return &TicketEmailDispatcher{pool: pool, outbox: NewTicketEmailOutbox(pool), sender: sender}
}

// ProcessOne claims one due job, builds the email from the paid order, then
// sends outside the database transaction. Its error contains no customer data.
func (d *TicketEmailDispatcher) ProcessOne(ctx context.Context) (bool, error) {
	if d == nil || d.pool == nil || d.sender == nil {
		return false, errors.New("ticket email dispatcher is not configured")
	}
	job, err := d.outbox.ClaimNext(ctx)
	if err != nil || job == nil {
		return false, err
	}
	recipient, name, orderUUID, tickets, err := d.loadPaidOrder(ctx, job.OrderID)
	if err != nil {
		if errors.Is(err, ErrTicketEmailNotEligible) {
			if markErr := d.outbox.MarkRejected(ctx, job, "invalid_order_data"); markErr != nil {
				return true, fmt.Errorf("ticket email job %d reject failed: %w", job.ID, markErr)
			}
			return true, fmt.Errorf("ticket email job %d rejected: invalid_order_data", job.ID)
		}
		if markErr := d.outbox.MarkFailure(ctx, job, "order_read_failed"); markErr != nil {
			return true, fmt.Errorf("ticket email job %d reject failed: %w", job.ID, markErr)
		}
		return true, fmt.Errorf("ticket email job %d scheduled for retry", job.ID)
	}
	suppressed, err := d.outbox.IsSuppressed(ctx, recipient)
	if err != nil {
		if markErr := d.outbox.MarkFailure(ctx, job, "suppression_read_failed"); markErr != nil {
			return true, fmt.Errorf("ticket email job %d retry scheduling failed: %w", job.ID, markErr)
		}
		return true, fmt.Errorf("ticket email job %d scheduled for retry", job.ID)
	}
	if suppressed {
		if err := d.outbox.MarkRejected(ctx, job, "recipient_suppressed"); err != nil {
			return true, fmt.Errorf("ticket email job %d suppression recording failed: %w", job.ID, err)
		}
		return true, fmt.Errorf("ticket email job %d suppressed", job.ID)
	}
	if err := d.sender.SendOrderTicketsEmailWithAttempt(recipient, name, orderUUID, tickets, job.AttemptID); err != nil {
		if markErr := d.outbox.MarkFailure(ctx, job, "smtp_send_failed"); markErr != nil {
			return true, fmt.Errorf("ticket email job %d retry scheduling failed: %w", job.ID, markErr)
		}
		return true, fmt.Errorf("ticket email job %d scheduled for retry", job.ID)
	}
	if err := d.outbox.MarkAccepted(ctx, job); err != nil {
		return true, fmt.Errorf("ticket email job %d acceptance recording failed: %w", job.ID, err)
	}
	return true, nil
}

func (d *TicketEmailDispatcher) loadPaidOrder(ctx context.Context, orderID int64) (string, string, string, []TicketEmailItem, error) {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", "", "", nil, err
	}
	defer tx.Rollback(ctx)
	var orderUUID, recipient, customerName, status, paymentStatus string
	err = tx.QueryRow(ctx, `
		SELECT public_uuid::text, customer_email::text, COALESCE(customer_name,''),
		       status::text, payment_status
		FROM billing.orders WHERE id=$1
	`, orderID).Scan(&orderUUID, &recipient, &customerName, &status, &paymentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", nil, ErrTicketEmailNotEligible
		}
		return "", "", "", nil, err
	}
	if status != "completed" || paymentStatus != "paid" || recipient == "" {
		return "", "", "", nil, ErrTicketEmailNotEligible
	}
	if customerName == "" {
		customerName = "Cliente osmi"
	}
	rows, err := tx.Query(ctx, `
		SELECT t.code, t.status::text, COALESCE(t.qr_code_data,''),
		       tt.name, e.name, e.starts_at, COALESCE(e.venue_name,'')
		FROM ticketing.tickets t
		JOIN ticketing.ticket_types tt ON tt.id=t.ticket_type_id
		JOIN ticketing.events e ON e.id=t.event_id
		WHERE t.order_id=$1 ORDER BY t.id
	`, orderID)
	if err != nil {
		return "", "", "", nil, err
	}
	defer rows.Close()
	var tickets []TicketEmailItem
	for rows.Next() {
		var code, ticketStatus, credential, ticketType, eventName, venue string
		var startsAt time.Time
		if err := rows.Scan(&code, &ticketStatus, &credential, &ticketType, &eventName, &startsAt, &venue); err != nil {
			return "", "", "", nil, err
		}
		if ticketStatus != "sold" && ticketStatus != "checked_in" {
			return "", "", "", nil, ErrTicketEmailNotEligible
		}
		if code == "" || strings.TrimSpace(credential) == "" {
			return "", "", "", nil, ErrTicketEmailNotEligible
		}
		qrBase64, err := qr.GenerateQRBase64(credential)
		if err != nil {
			return "", "", "", nil, ErrTicketEmailNotEligible
		}
		tickets = append(tickets, TicketEmailItem{
			TicketCode: code, TicketTypeName: ticketType, EventName: eventName,
			EventDate:     startsAt.Format("02/01/2006 15:04"),
			EventLocation: venue, QRBase64: qrBase64,
		})
	}
	if err := rows.Err(); err != nil {
		return "", "", "", nil, err
	}
	rows.Close()
	if len(tickets) == 0 {
		return "", "", "", nil, ErrTicketEmailNotEligible
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", nil, err
	}
	return recipient, customerName, orderUUID, tickets, nil
}
