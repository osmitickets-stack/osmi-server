package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
)

func testStripeSignature(payload []byte, secret string, timestamp int64) string {
	signedPayload := fmt.Sprintf("%d.%s", timestamp, payload)

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signedPayload))

	return fmt.Sprintf(
		"t=%d,v1=%s",
		timestamp,
		hex.EncodeToString(mac.Sum(nil)),
	)
}

func TestHandleWebhookConcurrentSameEventIsIdempotent(t *testing.T) {
	pool := requireIntegrationDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	paymentRepo := postgres.NewPaymentRepository(pool)
	orderRepo := postgres.NewOrderRepository(pool)
	ticketRepo := postgres.NewTicketRepository(pool)
	ticketTypeRepo := postgres.NewTicketTypeRepository(pool)

	const webhookSecret = "whsec_integration_test_only"

	service := NewPaymentService(
		paymentRepo,
		orderRepo,
		ticketRepo,
		ticketTypeRepo,
		nil,
		nil,
		webhookSecret,
		nil,
	)

	suffix := time.Now().UnixNano()

	organizerSlug := fmt.Sprintf("wh-org-%d", suffix)
	eventSlug := fmt.Sprintf("wh-event-%d", suffix)
	ticketCode := fmt.Sprintf("WH-%d", suffix)
	paymentIntentID := fmt.Sprintf("pi_integration_%d", suffix)
	stripeEventID := fmt.Sprintf("evt_integration_%d", suffix)

	// --------------------------------------------------------
	// ORGANIZER
	// --------------------------------------------------------

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
		"Webhook Integration Organizer",
		organizerSlug,
		"integration@example.com",
	).Scan(&organizerID)
	if err != nil {
		t.Fatalf("create organizer: %v", err)
	}

	// --------------------------------------------------------
	// EVENT
	// --------------------------------------------------------

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
		"Webhook Concurrency Event",
	).Scan(&eventID)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	// --------------------------------------------------------
	// TICKET TYPE
	// --------------------------------------------------------

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
		"Webhook Concurrent Ticket",
	).Scan(&ticketTypeID)
	if err != nil {
		t.Fatalf("create ticket type: %v", err)
	}

	// --------------------------------------------------------
	// ORDER
	// --------------------------------------------------------

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
			payment_status
		)
		VALUES (
			'integration@example.com',
			'Webhook Buyer',
			10.00,
			0,
			0,
			0,
			10.00,
			'MXN',
			'pending',
			'pending'
		)
		RETURNING id, public_uuid::text
		`,
	).Scan(&orderID, &orderPublicID)
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	// --------------------------------------------------------
	// ORDER ITEM
	// --------------------------------------------------------

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
		t.Fatalf("create order item: %v", err)
	}

	// --------------------------------------------------------
	// RESERVED TICKET
	// --------------------------------------------------------

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
			'integration-secret',
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
	).Scan(&ticketID)
	if err != nil {
		t.Fatalf("create reserved ticket: %v", err)
	}

	// --------------------------------------------------------
	// PAYMENT PROVIDER
	// --------------------------------------------------------

	var providerID int16

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO billing.payment_providers (
			code,
			name
		)
		VALUES ('iteststripe', 'Integration Stripe')
		ON CONFLICT (code)
		DO UPDATE SET name = EXCLUDED.name
		RETURNING id
		`,
	).Scan(&providerID)
	if err != nil {
		t.Fatalf("create payment provider: %v", err)
	}

	// --------------------------------------------------------
	// PAYMENT
	// --------------------------------------------------------

	var paymentID int64

	err = pool.QueryRow(
		ctx,
		`
		INSERT INTO billing.payments (
			order_id,
			provider_id,
			provider_transaction_id,
			amount,
			currency,
			status,
			payment_method
		)
		VALUES (
			$1,
			$2,
			$3,
			10.00,
			'MXN',
			'processing',
			'card'
		)
		RETURNING id
		`,
		orderID,
		providerID,
		paymentIntentID,
	).Scan(&paymentID)
	if err != nil {
		t.Fatalf("create payment: %v", err)
	}

	// --------------------------------------------------------
	// STRIPE EVENT PAYLOAD
	// --------------------------------------------------------

	payload, err := json.Marshal(map[string]interface{}{
		"id":     stripeEventID,
		"object": "event",
		"type":   "payment_intent.succeeded",
		"data": map[string]interface{}{
			"object": map[string]interface{}{
				"id":     paymentIntentID,
				"object": "payment_intent",
				"metadata": map[string]string{
					"order_id": orderPublicID,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}

	signature := testStripeSignature(
		payload,
		webhookSecret,
		time.Now().Unix(),
	)

	// --------------------------------------------------------
	// CLEANUP
	// --------------------------------------------------------

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM audit.stripe_events WHERE event_id = $1`,
			stripeEventID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM billing.payments WHERE id = $1`,
			paymentID,
		)

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

		_, _ = pool.Exec(
			cleanupCtx,
			`DELETE FROM billing.payment_providers WHERE id = $1`,
			providerID,
		)
	})

	// --------------------------------------------------------
	// TWO SIMULTANEOUS DELIVERIES OF THE SAME EVENT
	// --------------------------------------------------------

	start := make(chan struct{})
	errs := make(chan error, 2)

	run := func() {
		<-start

		runCtx, runCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer runCancel()

		errs <- service.HandleWebhook(
			runCtx,
			payload,
			signature,
		)
	}

	go run()
	go run()

	close(start)

	err1 := <-errs
	err2 := <-errs

	if err1 != nil {
		t.Fatalf("first concurrent webhook failed: %v", err1)
	}

	if err2 != nil {
		t.Fatalf("second concurrent webhook failed: %v", err2)
	}

	// --------------------------------------------------------
	// ASSERT STRIPE EVENT
	// --------------------------------------------------------

	var eventCount int
	var processed bool

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			COUNT(*),
			COALESCE(bool_and(processed_at IS NOT NULL), false)
		FROM audit.stripe_events
		WHERE event_id = $1
		`,
		stripeEventID,
	).Scan(&eventCount, &processed)
	if err != nil {
		t.Fatalf("read stripe event: %v", err)
	}

	if eventCount != 1 {
		t.Fatalf(
			"expected exactly 1 stripe event row, got %d",
			eventCount,
		)
	}

	if !processed {
		t.Fatal("stripe event was not marked processed")
	}

	// --------------------------------------------------------
	// ASSERT ORDER
	// --------------------------------------------------------

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
	).Scan(&orderStatus, &paymentStatus)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}

	if orderStatus != "completed" {
		t.Fatalf("expected completed order, got %q", orderStatus)
	}

	if paymentStatus != "paid" {
		t.Fatalf("expected paid order, got %q", paymentStatus)
	}

	// --------------------------------------------------------
	// ASSERT PAYMENT
	// --------------------------------------------------------

	var finalPaymentStatus string

	err = pool.QueryRow(
		ctx,
		`
		SELECT status
		FROM billing.payments
		WHERE id = $1
		`,
		paymentID,
	).Scan(&finalPaymentStatus)
	if err != nil {
		t.Fatalf("read payment: %v", err)
	}

	if finalPaymentStatus != "completed" {
		t.Fatalf(
			"expected completed payment, got %q",
			finalPaymentStatus,
		)
	}

	// --------------------------------------------------------
	// ASSERT TICKET + INVENTORY
	// --------------------------------------------------------

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
	).Scan(&totalTickets, &soldTickets)
	if err != nil {
		t.Fatalf("count tickets: %v", err)
	}

	if totalTickets != 1 || soldTickets != 1 {
		t.Fatalf(
			"expected exactly one sold ticket; total=%d sold=%d",
			totalTickets,
			soldTickets,
		)
	}

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
		t.Fatalf("read inventory: %v", err)
	}

	if reservedQuantity != 0 ||
		soldQuantity != 1 ||
		availableQuantity != 0 {

		t.Fatalf(
			"invalid inventory after concurrent webhooks: reserved=%d sold=%d available=%d",
			reservedQuantity,
			soldQuantity,
			availableQuantity,
		)
	}

	t.Logf(
		"same Stripe event delivered concurrently remained idempotent: event_rows=%d order=%s payment=%s tickets=%d sold=%d reserved=%d available=%d",
		eventCount,
		orderStatus,
		finalPaymentStatus,
		totalTickets,
		soldTickets,
		reservedQuantity,
		availableQuantity,
	)
}
