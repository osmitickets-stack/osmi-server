package services

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
)

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
		t.Fatalf("parse OSMI_TEST_DATABASE_URL: %v", err)
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

	var databaseName string
	if err := pool.QueryRow(
		ctx,
		`SELECT current_database()`,
	).Scan(&databaseName); err != nil {
		t.Fatalf("verify integration database: %v", err)
	}

	if databaseName != "osmidb_test" {
		t.Fatalf(
			"REFUSING TO RUN INTEGRATION TESTS: expected database osmidb_test, got %q",
			databaseName,
		)
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

	service := NewPaymentService(
		paymentRepo,
		orderRepo,
		ticketRepo,
		ticketTypeRepo,
		nil, // eventRepo: no se usa en ProcessPaidOrder
		nil, // stripeClient: no se usa en ProcessPaidOrder
		"",  // webhookSecret
		nil, // emailClient: evitamos enviar email real
	)

	// ========================================================
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

	t.Logf(
		"concurrent fulfillment remained idempotent: order=%s ticket=%d reserved=%d sold=%d available=%d",
		orderStatus,
		ticketID,
		reservedQuantity,
		soldQuantity,
		availableQuantity,
	)
}
