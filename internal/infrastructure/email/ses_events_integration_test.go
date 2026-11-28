package email

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run with an isolated PostgreSQL database prepared by the SES feedback test
// runner. The guard prevents this integration test from touching production.
func TestSESEventPostgresIntegration(t *testing.T) {
	url := os.Getenv("OSMI_ISOLATED_FEEDBACK_DATABASE_URL")
	if url == "" {
		t.Skip("isolated feedback database not configured")
	}
	port, err := strconv.Atoi(os.Getenv("OSMI_ISOLATED_FEEDBACK_PORT"))
	if err != nil {
		t.Fatal("invalid isolated database port")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil || config.ConnConfig.Host != "127.0.0.1" ||
		int(config.ConnConfig.Port) != port || config.ConnConfig.Database != "osmidb_test" ||
		config.ConnConfig.User != "postgres" {
		t.Fatal("refusing non-isolated database")
	}
	config.MaxConns = 12
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	defer pool.Close()
	var database, marker string
	if err := pool.QueryRow(ctx,
		"SELECT current_database(), marker FROM public.osmi_ses_feedback_test_guard",
	).Scan(&database, &marker); err != nil || database != "osmidb_test" ||
		marker != os.Getenv("OSMI_ISOLATED_FEEDBACK_MARKER") {
		t.Fatal("refusing database without disposable guard")
	}
	outbox := NewTicketEmailOutbox(pool)
	var nextOrderID int64

	type fixture struct {
		orderID int64
		jobID   int64
		attempt uuid.UUID
		address string
		message string
	}
	create := func(t *testing.T, address, status string) fixture {
		t.Helper()
		f := fixture{orderID: atomic.AddInt64(&nextOrderID, 1), attempt: uuid.New(),
			address: address, message: "synthetic-" + uuid.NewString()}
		if _, err := pool.Exec(ctx,
			"INSERT INTO billing.orders(id,customer_email) VALUES ($1,$2)",
			f.orderID, f.address); err != nil {
			t.Fatal("synthetic order insert failed:", err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO notifications.ticket_email_jobs
			 (order_id,status,attempts,current_attempt_id) VALUES ($1,$2,1,$3) RETURNING id`,
			f.orderID, status, f.attempt).Scan(&f.jobID); err != nil {
			t.Fatal("synthetic job insert failed:", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO notifications.ticket_email_attempts
			 (id,job_id,attempt_number,status,ses_message_id)
			 VALUES ($1,$2,1,$3,$4)`, f.attempt, f.jobID, status, f.message); err != nil {
			t.Fatal("synthetic attempt insert failed:", err)
		}
		return f
	}

	event := func(t *testing.T, f fixture, kind string) *SESEvent {
		t.Helper()
		data := map[string]any{
			"eventType": kind,
			"mail": map[string]any{
				"messageId":   f.message,
				"destination": []string{f.address},
				"tags":        map[string][]string{"osmi_attempt_id": {f.attempt.String()}},
			},
		}
		if kind == "Bounce" {
			data["bounce"] = map[string]any{
				"bounceType":        "Permanent",
				"bouncedRecipients": []map[string]string{{"emailAddress": f.address}},
			}
		} else if kind == "Complaint" {
			data["complaint"] = map[string]any{
				"complainedRecipients": []map[string]string{{"emailAddress": f.address}},
			}
		}
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeSESEvent(raw)
		if err != nil {
			t.Fatal("synthetic event decode failed:", err)
		}
		return decoded
	}

	type snapshot struct {
		jobStatus, attemptStatus, jobEventAt, jobUpdatedAt     string
		attemptEventAt, acceptedAt, reason, suppressionFirstAt string
		suppressionUpdatedAt                                   string
		attemptRows, suppressionRows                           int
	}
	state := func(t *testing.T, f fixture) snapshot {
		t.Helper()
		var s snapshot
		if err := pool.QueryRow(ctx, `
			SELECT j.status,a.status,
			 COALESCE(j.last_event_at::text,''),j.updated_at::text,
			 COALESCE(a.last_event_at::text,''),COALESCE(a.accepted_at::text,'')
			FROM notifications.ticket_email_jobs j
			JOIN notifications.ticket_email_attempts a ON a.job_id=j.id
			WHERE j.id=$1 AND a.id=$2`, f.jobID, f.attempt).Scan(
			&s.jobStatus, &s.attemptStatus, &s.jobEventAt, &s.jobUpdatedAt,
			&s.attemptEventAt, &s.acceptedAt,
		); err != nil {
			t.Fatal("synthetic state read failed:", err)
		}
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM notifications.ticket_email_attempts WHERE job_id=$1",
			f.jobID).Scan(&s.attemptRows); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT count(*),COALESCE(max(reason),''),
			 COALESCE(max(first_seen_at)::text,''),COALESCE(max(updated_at)::text,'')
			FROM notifications.email_suppressions WHERE recipient_sha256=$1`,
			recipientHash(f.address)).Scan(&s.suppressionRows, &s.reason,
			&s.suppressionFirstAt, &s.suppressionUpdatedAt); err != nil {
			t.Fatal(err)
		}
		return s
	}
	check := func(t *testing.T, f fixture, job, attempt, reason string, suppressed bool) {
		t.Helper()
		s := state(t, f)
		wantRows := 0
		if suppressed {
			wantRows = 1
		}
		actualSuppressed, err := outbox.IsSuppressed(ctx, f.address)
		if err != nil || s.jobStatus != job || s.attemptStatus != attempt ||
			s.reason != reason || s.suppressionRows != wantRows ||
			s.attemptRows != 1 || actualSuppressed != suppressed {
			t.Fatalf("unexpected synthetic state: job=%s attempt=%s reason=%s suppression_rows=%d attempt_rows=%d",
				s.jobStatus, s.attemptStatus, s.reason, s.suppressionRows, s.attemptRows)
		}
	}
	record := func(t *testing.T, e *SESEvent) {
		t.Helper()
		if err := outbox.RecordSESEvent(ctx, e); err != nil {
			t.Fatal("synthetic feedback processing failed:", err)
		}
	}
	assertUnchanged := func(t *testing.T, f fixture, before snapshot) {
		t.Helper()
		if after := state(t, f); !reflect.DeepEqual(before, after) {
			t.Fatalf("duplicate or stale event changed state: before=%+v after=%+v", before, after)
		}
	}

	t.Run("PermanentBounceAndDuplicate", func(t *testing.T) {
		f := create(t, "bounce-test@example.invalid", "accepted")
		e := event(t, f, "Bounce")
		record(t, e)
		check(t, f, "bounced", "bounced", "bounce", true)
		before := state(t, f)
		time.Sleep(15 * time.Millisecond)
		record(t, e)
		assertUnchanged(t, f, before)
	})

	t.Run("ComplaintAndDuplicate", func(t *testing.T) {
		f := create(t, "complaint-test@example.invalid", "delivered")
		e := event(t, f, "Complaint")
		record(t, e)
		check(t, f, "complained", "complained", "complaint", true)
		before := state(t, f)
		time.Sleep(15 * time.Millisecond)
		record(t, e)
		assertUnchanged(t, f, before)
	})

	t.Run("OutOfOrderAndComplaintPriority", func(t *testing.T) {
		f := create(t, "reorder-test@example.invalid", "accepted")
		bounce, delivered, complaint := event(t, f, "Bounce"), event(t, f, "Delivery"), event(t, f, "Complaint")
		record(t, bounce)
		check(t, f, "bounced", "bounced", "bounce", true)
		record(t, delivered)
		check(t, f, "delivered", "delivered", "bounce", true)
		record(t, complaint)
		check(t, f, "complained", "complained", "complaint", true)
		before := state(t, f)
		time.Sleep(15 * time.Millisecond)
		record(t, bounce)
		record(t, delivered)
		record(t, event(t, f, "Send"))
		assertUnchanged(t, f, before)
	})

	t.Run("DeliveryBeforeLateBounce", func(t *testing.T) {
		f := create(t, "late-bounce-test@example.invalid", "delivered")
		before := state(t, f)
		bounce := event(t, f, "Bounce")
		record(t, bounce)
		check(t, f, "delivered", "delivered", "bounce", true)
		after := state(t, f)
		if after.jobEventAt != before.jobEventAt ||
			after.jobUpdatedAt != before.jobUpdatedAt ||
			after.attemptEventAt != before.attemptEventAt {
			t.Fatal("late bounce downgraded delivered state or audit timestamps")
		}
		time.Sleep(15 * time.Millisecond)
		record(t, bounce)
		assertUnchanged(t, f, after)
	})

	t.Run("OtherRecipientCannotChangeState", func(t *testing.T) {
		f := create(t, "buyer-test@example.invalid", "accepted")
		other := create(t, "other-test@example.invalid", "accepted")
		before, otherBefore := state(t, f), state(t, other)
		wrongFeedback := event(t, f, "Bounce")
		wrongFeedback.Bounce.BouncedRecipients[0].EmailAddress = other.address
		if err := outbox.RecordSESEvent(ctx, wrongFeedback); err == nil {
			t.Fatal("feedback for another recipient was accepted")
		}
		wrongEnvelope := event(t, f, "Complaint")
		wrongEnvelope.Mail.Destination = []string{other.address}
		if err := outbox.RecordSESEvent(ctx, wrongEnvelope); err == nil {
			t.Fatal("envelope for another recipient was accepted")
		}
		assertUnchanged(t, f, before)
		assertUnchanged(t, other, otherBefore)
		missingRecipient := event(t, f, "Complaint")
		missingRecipient.Complaint.ComplainedRecipients = nil
		record(t, missingRecipient)
		check(t, f, "complained", "complained", "complaint", true)
		assertUnchanged(t, other, otherBefore)
	})

	t.Run("SimulatorExemption", func(t *testing.T) {
		f := create(t, "bounce+osmi-test@simulator.amazonses.com", "accepted")
		record(t, event(t, f, "Bounce"))
		check(t, f, "bounced", "bounced", "", false)
	})

	t.Run("ConcurrentBounceAndComplaint", func(t *testing.T) {
		f := create(t, "concurrent-test@example.invalid", "accepted")
		bounce, complaint := event(t, f, "Bounce"), event(t, f, "Complaint")
		var wg sync.WaitGroup
		errors := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				e := bounce
				if i%2 == 0 {
					e = complaint
				}
				if err := outbox.RecordSESEvent(ctx, e); err != nil {
					errors <- fmt.Errorf("concurrent feedback %d: %w", i, err)
				}
			}(i)
		}
		wg.Wait()
		close(errors)
		for err := range errors {
			t.Fatal(err)
		}
		check(t, f, "complained", "complained", "complaint", true)
		before := state(t, f)
		time.Sleep(15 * time.Millisecond)
		var duplicateWG sync.WaitGroup
		duplicates := make(chan error, 8)
		for i := 0; i < 8; i++ {
			duplicateWG.Add(1)
			go func() {
				defer duplicateWG.Done()
				if err := outbox.RecordSESEvent(ctx, complaint); err != nil {
					duplicates <- err
				}
			}()
		}
		duplicateWG.Wait()
		close(duplicates)
		for err := range duplicates {
			t.Fatal(err)
		}
		assertUnchanged(t, f, before)
	})
}
