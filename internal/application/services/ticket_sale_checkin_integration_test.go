package services

import (
	"context"
	"testing"
	"time"

	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
)

// The payment provider is intentionally absent: a fictitious paid order is
// fulfilled by the production service, then its signed ticket is scanned.
func TestPaidOrderToCheckInIntegration(t *testing.T) {
	checkIn, signer := newCheckInTestService(t)
	fixture := createCheckInFixture(t, signer, "reserved", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var orderID int64
	var orderPublicID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing.orders (
			customer_email, customer_name, subtotal, tax_amount,
			service_fee_amount, discount_amount, total_amount, currency,
			status, payment_status, paid_at
		) VALUES (
			'isolated-sale@example.invalid', 'Isolated Buyer', 10, 0,
			0, 0, 10, 'MXN', 'pending', 'paid', NOW()
		) RETURNING id, public_uuid::text
	`).Scan(&orderID, &orderPublicID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanupCtx, `UPDATE ticketing.tickets SET order_id=NULL WHERE id=$1`, fixture.TicketID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing.order_items WHERE order_id=$1`, orderID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing.orders WHERE id=$1`, orderID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing.order_items (
			order_id, ticket_type_id, quantity, unit_price, total_price, base_price
		) VALUES ($1, $2, 1, 10, 10, 10)
	`, orderID, fixture.TicketTypeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE ticketing.ticket_types
		SET reserved_quantity=1, sold_quantity=0
		WHERE id=$1
	`, fixture.TicketTypeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE ticketing.tickets
		SET order_id=$1, reserved_at=NOW(), reservation_expires_at=NOW()+INTERVAL '15 minutes'
		WHERE id=$2
	`, orderID, fixture.TicketID); err != nil {
		t.Fatal(err)
	}

	payment := NewPaymentService(
		postgres.NewPaymentRepository(pool),
		postgres.NewOrderRepository(pool),
		postgres.NewTicketRepository(pool),
		postgres.NewTicketTypeRepository(pool),
		nil, nil, "", nil,
	)
	if err := payment.ProcessPaidOrder(ctx, orderPublicID); err != nil {
		t.Fatalf("fulfill fictitious paid order: %v", err)
	}

	var orderStatus, status, qr string
	var count int
	var checkedIn bool
	if err := pool.QueryRow(ctx, `
		SELECT o.status::text, t.status, t.qr_code_data, t.validation_count,
			t.checked_in_at IS NOT NULL
		FROM billing.orders o JOIN ticketing.tickets t ON t.order_id=o.id
		WHERE o.id=$1
	`, orderID).Scan(&orderStatus, &status, &qr, &count, &checkedIn); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "completed" || status != "sold" || qr != fixture.Credential || count != 0 || checkedIn {
		t.Fatal("paid order did not preserve a signed, unused sold ticket")
	}

	principal := checkInAuthorizedContext(fixture)
	validated, err := checkIn.VerifyTicketCredential(principal, fixture.Credential, fixture.EventPublicID)
	if err != nil || validated == nil || !validated.CanCheckIn {
		t.Fatalf("validate sold ticket: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status, validation_count, checked_in_at IS NOT NULL FROM ticketing.tickets WHERE id=$1`, fixture.TicketID).Scan(&status, &count, &checkedIn); err != nil {
		t.Fatal(err)
	}
	if status != "sold" || count != 0 || checkedIn {
		t.Fatal("Validate consumed the ticket")
	}

	req := &ticketdto.CheckInTicketRequest{Credential: fixture.Credential, EventID: fixture.EventPublicID, Method: "qr_code"}
	first, err := checkIn.CheckInTicket(principal, req)
	if err != nil || first == nil || !first.Accepted {
		t.Fatalf("first check-in: %v", err)
	}
	second, err := checkIn.CheckInTicket(principal, req)
	if err != nil || second == nil || second.Accepted || second.Result != "ALREADY_CHECKED_IN" {
		t.Fatalf("second check-in was not rejected: %v", err)
	}
	var actorID int64
	if err := pool.QueryRow(ctx, `
		SELECT status, validation_count, checked_in_at IS NOT NULL, checked_in_by
		FROM ticketing.tickets WHERE id=$1
	`, fixture.TicketID).Scan(&status, &count, &checkedIn, &actorID); err != nil {
		t.Fatal(err)
	}
	if status != "checked_in" || count != 1 || !checkedIn || actorID != fixture.UserID {
		t.Fatal("ticket did not record exactly one authorized check-in")
	}
}
