package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/email"
	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

type fakeTicketEmailSender struct {
	attempts []uuid.UUID
	failNext bool
}

func (s *fakeTicketEmailSender) SendOrderTicketsEmailWithAttempt(
	_ string, _ string, _ string, tickets []email.TicketEmailItem, attempt uuid.UUID,
) error {
	if len(tickets) != 1 || tickets[0].QRBase64 == "" || attempt == uuid.Nil {
		return errors.New("invalid ticket email fixture")
	}
	s.attempts = append(s.attempts, attempt)
	if s.failNext {
		s.failNext = false
		return errors.New("simulated SMTP failure")
	}
	return nil
}

func requireIntegrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("OSMI_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OSMI_TEST_DATABASE_URL is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("REFUSING integration tests: invalid test database URL")
	}
	if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 55439 ||
		config.ConnConfig.Database != "osmidb_test" || config.ConnConfig.User != "osmi" {
		t.Fatal("REFUSING integration tests: expected isolated PostgreSQL at 127.0.0.1:55439 as osmi/osmidb_test")
	}

	// Necesitaremos varias conexiones reales para las pruebas
	// de concurrencia posteriores.
	config.MaxConns = 10
	config.MinConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}

	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping integration database: %v", err)
	}

	var databaseName, databaseUser, marker string
	var markerCount, migrationVersion int
	var migrationDirty bool
	if err := pool.QueryRow(
		ctx,
		`SELECT current_database(), current_user,
			(SELECT count(*) FROM public.osmi_checkin_test_guard),
			(SELECT marker FROM public.osmi_checkin_test_guard LIMIT 1),
			(SELECT version FROM public.schema_migrations LIMIT 1),
			(SELECT dirty FROM public.schema_migrations LIMIT 1)`,
	).Scan(&databaseName, &databaseUser, &markerCount, &marker, &migrationVersion, &migrationDirty); err != nil {
		t.Fatal("REFUSING integration tests: isolated database guard unavailable")
	}

	if databaseName != "osmidb_test" || databaseUser != "osmi" ||
		markerCount != 1 || marker != "osmi-checkin-isolated-v1" || migrationVersion != 14 || migrationDirty {
		t.Fatal("REFUSING integration tests: database identity or schema guard mismatch")
	}

	return pool
}

func TestIntegrationDBGuard(t *testing.T) {
	pool := requireIntegrationDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var databaseName string
	if err := pool.QueryRow(
		ctx,
		`SELECT current_database()`,
	).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}

	if databaseName != "osmidb_test" {
		t.Fatalf("expected osmidb_test, got %q", databaseName)
	}
}

func TestProcessPaidOrderConcurrentIsIdempotent(t *testing.T) {
	pool := requireIntegrationDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// ========================================================
	// REPOSITORIES + SERVICE
	// ========================================================

	paymentRepo := postgres.NewPaymentRepository(pool)
	orderRepo := postgres.NewOrderRepository(pool)
	ticketRepo := postgres.NewTicketRepository(pool)
	ticketTypeRepo := postgres.NewTicketTypeRepository(pool)
	signer, signerErr := security.NewTicketCredentialService("test-k1", []byte("0123456789abcdef0123456789abcdef"))
	if signerErr != nil {
		t.Fatal(signerErr)
	}

	service := NewPaymentService(
		paymentRepo,
		orderRepo,
		ticketRepo,
		ticketTypeRepo,
		nil, // eventRepo: no se usa en ProcessPaidOrder
		nil, // stripeClient: no se usa en ProcessPaidOrder
		"",  // webhookSecret
		nil, // emailClient: evitamos enviar email real
		signer,
	)

	// ========================================================
	service.SetTicketEmailOutbox(email.NewTicketEmailOutbox(pool))

	// UNIQUE FIXTURE DATA
	// ========================================================

	suffix := time.Now().UnixNano()

	organizerSlug := fmt.Sprintf("integration-organizer-%d", suffix)
	eventSlug := fmt.Sprintf("integration-event-%d", suffix)
	ticketCode := fmt.Sprintf("INT-%d", suffix)

	var organizerID int64

	err := pool.QueryRow(
		ctx,
		`
		INSERT INTO ticketing.organizers (
			name,
			slug,
			contact_email
		)
		VALUES ($1, $2, $3)
		RETURNING id
		`,
		"Integration Organizer",
		organizerSlug,
		"integration@example.com",
	).Scan(&organizerID)
	if err != nil {
		t.Fatalf("create organizer fixture: %v", err)
	}

	var eventID int64

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO ticketing.events (
			organizer_id,
			slug,
			name,
			starts_at,
			ends_at
		)
		VALUES (
			$1,
			$2,
			$3,
			NOW() + INTERVAL '1 day',
			NOW() + INTERVAL '1 day 2 hours'
		)
		RETURNING id
		`,
		organizerID,
		eventSlug,
		"Integration Concurrent Event",
	).Scan(&eventID)
	if err != nil {
		t.Fatalf("create event fixture: %v", err)
	}

	var ticketTypeID int64

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO ticketing.ticket_types (
			event_id,
			name,
			base_price,
			currency,
			total_quantity,
			reserved_quantity,
			sold_quantity
		)
		VALUES ($1, $2, 10.00, 'MXN', 1, 1, 0)
		RETURNING id
		`,
		eventID,
		"Integration Concurrent Ticket",
	).Scan(&ticketTypeID)
	if err != nil {
		t.Fatalf("create ticket type fixture: %v", err)
	}

	var orderID int64
	var orderPublicID string

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO billing.orders (
			customer_email,
			customer_name,
			subtotal,
			tax_amount,
			service_fee_amount,
			discount_amount,
			total_amount,
			currency,
			status,
			payment_status,
			paid_at
		)
		VALUES (
			$1,
			$2,
			10.00,
			0,
			0,
			0,
			10.00,
			'MXN',
			'pending',
			'paid',
			NOW()
		)
		RETURNING id, public_uuid::text
		`,
		"integration@example.com",
		"Integration Buyer",
	).Scan(&orderID, &orderPublicID)
	if err != nil {
		t.Fatalf("create order fixture: %v", err)
	}

	_, err = pool.Exec(
		ctx,
		`
		INSERT INTO billing.order_items (
			order_id,
			ticket_type_id,
			quantity,
			unit_price,
			total_price,
			base_price
		)
		VALUES ($1, $2, 1, 10.00, 10.00, 10.00)
		`,
		orderID,
		ticketTypeID,
	)
	if err != nil {
		t.Fatalf("create order item fixture: %v", err)
	}

	var ticketID int64

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO ticketing.tickets (
			ticket_type_id,
			event_id,
			order_id,
			code,
			secret_hash,
			status,
			final_price,
			currency,
			reserved_at,
			reservation_expires_at
		)
		VALUES (
			$1,
			$2,
			$3,
			$4,
			$5,
			'reserved',
			10.00,
			'MXN',
			NOW(),
			NOW() + INTERVAL '5 minutes'
		)
		RETURNING id
		`,
		ticketTypeID,
		eventID,
		orderID,
		ticketCode,
		"integration-secret-hash",
	).Scan(&ticketID)
	if err != nil {
		t.Fatalf("create ticket fixture: %v", err)
	}

	// ========================================================
	// CLEANUP
	// ========================================================

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM ticketing.tickets WHERE id = $1`,
			ticketID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM billing.order_items WHERE order_id = $1`,
			orderID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM billing.orders WHERE id = $1`,
			orderID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM ticketing.ticket_types WHERE id = $1`,
			ticketTypeID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM ticketing.events WHERE id = $1`,
			eventID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM ticketing.organizers WHERE id = $1`,
			organizerID,
		)
	})

	// ========================================================
	// CONCURRENT FULFILLMENT
	// ========================================================

	start := make(chan struct{})
	errs := make(chan error, 2)

	run := func() {
		<-start

		runCtx, runCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer runCancel()

		errs <- service.ProcessPaidOrder(
			runCtx,
			orderPublicID,
		)
	}

	go run()
	go run()

	// Liberar ambas goroutines al mismo tiempo.
	close(start)

	err1 := <-errs
	err2 := <-errs

	if err1 != nil {
		t.Fatalf("first concurrent ProcessPaidOrder failed: %v", err1)
	}

	if err2 != nil {
		t.Fatalf("second concurrent ProcessPaidOrder failed: %v", err2)
	}

	// ========================================================
	// ASSERT ORDER
	// ========================================================

	var orderStatus string
	var paymentStatus string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status::text, payment_status
		FROM billing.orders
		WHERE id = $1
		`,
		orderID,
	).Scan(
		&orderStatus,
		&paymentStatus,
	)
	if err != nil {
		t.Fatalf("read final order: %v", err)
	}

	if orderStatus != "completed" {
		t.Fatalf(
			"expected order status completed, got %q",
			orderStatus,
		)
	}

	if paymentStatus != "paid" {
		t.Fatalf(
			"expected payment status paid, got %q",
			paymentStatus,
		)
	}

	// ========================================================
	// ASSERT TICKET
	// ========================================================

	var totalTickets int
	var soldTickets int

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'sold')
		FROM ticketing.tickets
		WHERE order_id = $1
		`,
		orderID,
	).Scan(
		&totalTickets,
		&soldTickets,
	)
	if err != nil {
		t.Fatalf("count final tickets: %v", err)
	}

	if totalTickets != 1 {
		t.Fatalf(
			"expected exactly 1 ticket, got %d",
			totalTickets,
		)
	}

	if soldTickets != 1 {
		t.Fatalf(
			"expected exactly 1 sold ticket, got %d",
			soldTickets,
		)
	}

	// ========================================================
	// ASSERT INVENTORY
	// ========================================================

	var reservedQuantity int
	var soldQuantity int
	var availableQuantity int

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			reserved_quantity,
			sold_quantity,
			available_quantity
		FROM ticketing.ticket_types
		WHERE id = $1
		`,
		ticketTypeID,
	).Scan(
		&reservedQuantity,
		&soldQuantity,
		&availableQuantity,
	)
	if err != nil {
		t.Fatalf("read final inventory: %v", err)
	}

	if reservedQuantity != 0 {
		t.Fatalf(
			"expected reserved_quantity 0, got %d",
			reservedQuantity,
		)
	}

	if soldQuantity != 1 {
		t.Fatalf(
			"expected sold_quantity 1, got %d",
			soldQuantity,
		)
	}

	if availableQuantity != 0 {
		t.Fatalf(
			"expected available_quantity 0, got %d",
			availableQuantity,
		)
	}
	var emailJobs int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications.ticket_email_jobs WHERE order_id=$1
	`, orderID).Scan(&emailJobs); err != nil {
		t.Fatalf("read ticket email outbox: %v", err)
	}
	if emailJobs != 1 {
		t.Fatalf("concurrent fulfillment created %d email jobs; want one", emailJobs)
	}
	sender := &fakeTicketEmailSender{failNext: true}
	dispatcher := email.NewTicketEmailDispatcher(pool, sender)
	processed, dispatchErr := dispatcher.ProcessOne(ctx)
	if !processed || dispatchErr == nil {
		t.Fatalf("expected one retryable SMTP failure: processed=%v err=%v", processed, dispatchErr)
	}
	var jobStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM notifications.ticket_email_jobs WHERE order_id=$1
	`, orderID).Scan(&jobStatus); err != nil || jobStatus != "retrying" {
		t.Fatalf("SMTP failure did not persist retry: status=%q err=%v", jobStatus, err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE notifications.ticket_email_jobs
		SET next_attempt_at=NOW() WHERE order_id=$1 AND status='retrying'
	`, orderID); err != nil {
		t.Fatal(err)
	}
	processed, dispatchErr = dispatcher.ProcessOne(ctx)
	if !processed || dispatchErr != nil || len(sender.attempts) != 2 {
		t.Fatalf("email retry failed: processed=%v err=%v attempts=%d", processed, dispatchErr, len(sender.attempts))
	}
	event := &email.SESEvent{EventType: "Delivery", AttemptID: sender.attempts[1]}
	event.Mail.MessageID = "ses-integration-delivery"
	if err := email.NewTicketEmailOutbox(pool).RecordSESEvent(ctx, event); err != nil {
		t.Fatalf("record SES delivery: %v", err)
	}
	if err := email.NewTicketEmailOutbox(pool).RecordSESEvent(ctx, event); err != nil {
		t.Fatalf("duplicate SES delivery: %v", err)
	}
	var attemptCount int
	if err := pool.QueryRow(ctx, `
		SELECT j.status, count(a.id)
		FROM notifications.ticket_email_jobs j
		JOIN notifications.ticket_email_attempts a ON a.job_id=j.id
		WHERE j.order_id=$1 GROUP BY j.id
	`, orderID).Scan(&jobStatus, &attemptCount); err != nil || jobStatus != "delivered" || attemptCount != 2 {
		t.Fatalf("email delivery state mismatch: status=%q attempts=%d err=%v", jobStatus, attemptCount, err)
	}
	complaintPayload := []byte(fmt.Sprintf(`{
		"eventType":"Complaint",
		"mail":{"messageId":"ses-integration-delivery","destination":["integration@example.com"],
		        "tags":{"osmi_attempt_id":["%s"]}},
		"complaint":{"complainedRecipients":[{"emailAddress":"integration@example.com"}]}
	}`, sender.attempts[1]))
	complaint, err := email.DecodeSESEvent(complaintPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := email.NewTicketEmailOutbox(pool).RecordSESEvent(ctx, complaint); err != nil {
		t.Fatalf("record SES complaint: %v", err)
	}
	suppressed, err := email.NewTicketEmailOutbox(pool).IsSuppressed(ctx, "Integration@Example.com")
	if err != nil || !suppressed {
		t.Fatalf("complaint did not suppress recipient: suppressed=%v err=%v", suppressed, err)
	}
	sum := sha256.Sum256([]byte("integration@example.com"))
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = pool.Exec(cleanup, `DELETE FROM notifications.email_suppressions WHERE recipient_sha256=$1`, hex.EncodeToString(sum[:]))
	})
	if err := pool.QueryRow(ctx, `
		SELECT status FROM notifications.ticket_email_jobs WHERE order_id=$1
	`, orderID).Scan(&jobStatus); err != nil || jobStatus != "complained" {
		t.Fatalf("complaint state mismatch: status=%q err=%v", jobStatus, err)
	}
	wrongRecipientPayload := []byte(fmt.Sprintf(`{
		"eventType":"Bounce",
		"mail":{"messageId":"ses-integration-delivery","destination":["other@example.invalid"],
		        "tags":{"osmi_attempt_id":["%s"]}},
		"bounce":{"bouncedRecipients":[{"emailAddress":"other@example.invalid"}]}
	}`, sender.attempts[1]))
	wrongRecipient, err := email.DecodeSESEvent(wrongRecipientPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := email.NewTicketEmailOutbox(pool).RecordSESEvent(ctx, wrongRecipient); err == nil {
		t.Fatal("mismatched SES recipient was accepted")
	}

	t.Logf(
		"concurrent fulfillment remained idempotent: order=%s ticket=%d reserved=%d sold=%d available=%d",
		orderStatus,
		ticketID,
		reservedQuantity,
		soldQuantity,
		availableQuantity,
	)
}
