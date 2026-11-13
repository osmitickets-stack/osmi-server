package services

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	paymentdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/payment"
	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/stripe/stripe-go/v83"
)

type raceStripeClient struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	once    sync.Once
	creates int
	intent  *stripe.PaymentIntent
}

func (c *raceStripeClient) CreatePaymentIntent(int64, string, string, int) (*stripe.PaymentIntent, error) {
	c.mu.Lock()
	c.creates++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	<-c.release
	return c.intent, nil
}

func (c *raceStripeClient) GetPaymentIntent(string) (*stripe.PaymentIntent, error) {
	return c.intent, nil
}

func ensureStripeTestProvider(t *testing.T, pool *pgxpool.Pool) int16 {
	t.Helper()
	ctx := context.Background()
	var providerID int16
	err := pool.QueryRow(ctx, `INSERT INTO billing.payment_providers(code,name) VALUES ('stripe','Isolated Stripe Test') ON CONFLICT (code) DO NOTHING RETURNING id`).Scan(&providerID)
	if err == nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = pool.Exec(cleanup, `DELETE FROM billing.payment_providers WHERE id=$1 AND code='stripe'`, providerID)
		})
		return providerID
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM billing.payment_providers WHERE code='stripe' AND is_active=true`).Scan(&providerID); err != nil {
		t.Fatal("active isolated Stripe provider unavailable")
	}
	return providerID
}

func concurrencyTicketType(t *testing.T, pool *pgxpool.Pool, total int) int64 {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	var organizerID, eventID, typeID int64
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.organizers(name,slug,contact_email) VALUES ('Concurrency Organizer',$1,'integration@example.invalid') RETURNING id`, fmt.Sprintf("race-org-%d", suffix)).Scan(&organizerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.events(organizer_id,slug,name,starts_at,ends_at) VALUES ($1,$2,'Concurrency Event',NOW()+INTERVAL '1 day',NOW()+INTERVAL '1 day 2 hours') RETURNING id`, organizerID, fmt.Sprintf("race-event-%d", suffix)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.ticket_types(event_id,name,base_price,currency,total_quantity,reserved_quantity,sold_quantity) VALUES ($1,'Concurrency Ticket',10,'MXN',$2,0,0) RETURNING id`, eventID, total).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.tickets WHERE ticket_type_id=$1`, typeID)
		_, _ = pool.Exec(cleanup, `DELETE FROM billing.payments WHERE order_id IN (SELECT id FROM billing.orders WHERE customer_email=$1)`, fmt.Sprintf("race-%d@example.invalid", typeID))
		_, _ = pool.Exec(cleanup, `DELETE FROM billing.orders WHERE id IN (SELECT id FROM billing.orders WHERE customer_email=$1)`, fmt.Sprintf("race-%d@example.invalid", typeID))
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.ticket_types WHERE id=$1`, typeID)
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.events WHERE id=$1`, eventID)
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.organizers WHERE id=$1`, organizerID)
	})
	return typeID
}

func TestLastTicketConcurrentReservationsIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id := concurrencyTicketType(t, pool, 1)
	repo := postgres.NewTicketTypeRepository(pool)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, err := pool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)
			if err := repo.ReserveTicketWithLock(ctx, tx, id, 1); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(ctx)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	success, rejected := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			rejected++
		}
	}
	var reserved, sold, total int
	if err := pool.QueryRow(ctx, `SELECT reserved_quantity,sold_quantity,total_quantity FROM ticketing.ticket_types WHERE id=$1`, id).Scan(&reserved, &sold, &total); err != nil {
		t.Fatal(err)
	}
	if success != 1 || rejected != 1 || reserved != 1 || sold != 0 || total != 1 {
		t.Fatalf("oversell: successes=%d rejected=%d inventory=%d/%d/%d", success, rejected, reserved, sold, total)
	}
}

func TestConcurrentExpiryPreservesPaidInventoryIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id := concurrencyTicketType(t, pool, 2)
	var eventID int64
	if err := pool.QueryRow(ctx, `SELECT event_id FROM ticketing.ticket_types WHERE id=$1`, id).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"pending", "paid"} {
		var orderID int64
		if err := pool.QueryRow(ctx, `INSERT INTO billing.orders(customer_email,customer_name,subtotal,tax_amount,service_fee_amount,discount_amount,total_amount,currency,status,payment_status) VALUES ($1,'Test Buyer',10,0,0,0,10,'MXN','pending',$2) RETURNING id`, fmt.Sprintf("race-%d@example.invalid", id), status).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,reserved_at,reservation_expires_at) VALUES ($1,$2,$3,$4,'test-only','reserved',10,'MXN',NOW()-INTERVAL '20 minutes',NOW()-INTERVAL '5 minutes')`, id, eventID, orderID, fmt.Sprintf("RACE-%d-%d", id, i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE ticketing.ticket_types SET reserved_quantity=2 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewTicketTypeRepository(pool)
	start := make(chan struct{})
	counts := make(chan int64, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, err := pool.Begin(ctx)
			if err != nil {
				errors <- err
				return
			}
			defer tx.Rollback(ctx)
			n, err := repo.ReleaseExpiredReservationsTx(ctx, tx)
			if err != nil {
				errors <- err
				return
			}
			if err = tx.Commit(ctx); err != nil {
				errors <- err
				return
			}
			counts <- n
		}()
	}
	close(start)
	wg.Wait()
	close(counts)
	close(errors)
	for err := range errors {
		t.Errorf("expiry error: %v", err)
	}
	var released int64
	for n := range counts {
		released += n
	}
	var reserved, expired, paidReserved int
	if err := pool.QueryRow(ctx, `SELECT tt.reserved_quantity, count(*) FILTER (WHERE t.status='expired'), count(*) FILTER (WHERE o.payment_status='paid' AND t.status='reserved') FROM ticketing.ticket_types tt JOIN ticketing.tickets t ON t.ticket_type_id=tt.id LEFT JOIN billing.orders o ON o.id=t.order_id WHERE tt.id=$1 GROUP BY tt.reserved_quantity`, id).Scan(&reserved, &expired, &paidReserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 || expired != 1 || paidReserved != 1 {
		t.Fatalf("expiry mismatch: released=%d reserved=%d expired=%d paid_reserved=%d", released, reserved, expired, paidReserved)
	}
}

func TestPaymentWinsExpiryRaceIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id := concurrencyTicketType(t, pool, 1)
	var eventID, orderID int64
	if err := pool.QueryRow(ctx, `SELECT event_id FROM ticketing.ticket_types WHERE id=$1`, id).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.orders(customer_email,customer_name,subtotal,tax_amount,service_fee_amount,discount_amount,total_amount,currency,status,payment_status) VALUES ($1,'Test Buyer',10,0,0,0,10,'MXN','pending','pending') RETURNING id`, fmt.Sprintf("race-%d@example.invalid", id)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,reserved_at,reservation_expires_at) VALUES ($1,$2,$3,$4,'test-only','reserved',10,'MXN',NOW()-INTERVAL '20 minutes',NOW()-INTERVAL '5 minutes')`, id, eventID, orderID, fmt.Sprintf("RACE-PAY-%d", id)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ticketing.ticket_types SET reserved_quantity=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}

	// Hold the same order lock used by the payment writer. Expiry skips this
	// order, then a later pass sees the paid state and keeps the reservation.
	paymentTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer paymentTx.Rollback(ctx)
	var lockedID int64
	if err := paymentTx.QueryRow(ctx, `SELECT id FROM billing.orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	type result struct {
		count int64
		err   error
	}
	finished := make(chan result, 1)
	go func() {
		close(started)
		n, err := postgres.NewTicketTypeRepository(pool).ReleaseExpiredReservations(ctx)
		finished <- result{n, err}
	}()
	<-started
	if _, err := paymentTx.Exec(ctx, `UPDATE billing.orders SET payment_status='paid', paid_at=NOW() WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	if err := paymentTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-finished
	if r.err != nil {
		t.Fatalf("paid order expired: count=%d error=%v", r.count, r.err)
	}
	var reserved int
	var status string
	if err := pool.QueryRow(ctx, `SELECT tt.reserved_quantity,t.status FROM ticketing.ticket_types tt JOIN ticketing.tickets t ON t.ticket_type_id=tt.id WHERE tt.id=$1`, id).Scan(&reserved, &status); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 || status != "reserved" {
		t.Fatalf("paid reservation changed: inventory=%d ticket=%s", reserved, status)
	}
}

func TestExpiryKeepsInFlightPaymentIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	providerID := ensureStripeTestProvider(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id := concurrencyTicketType(t, pool, 1)
	var eventID, orderID int64
	if err := pool.QueryRow(ctx, `SELECT event_id FROM ticketing.ticket_types WHERE id=$1`, id).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.orders(customer_email,customer_name,subtotal,tax_amount,service_fee_amount,discount_amount,total_amount,currency,status,payment_status) VALUES ($1,'Test Buyer',10,0,0,0,10,'MXN','pending','pending') RETURNING id`, fmt.Sprintf("race-%d@example.invalid", id)).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,reserved_at,reservation_expires_at) VALUES ($1,$2,$3,$4,'test-only','reserved',10,'MXN',NOW()-INTERVAL '20 minutes',NOW()-INTERVAL '5 minutes')`, id, eventID, orderID, fmt.Sprintf("RACE-FLIGHT-%d", id)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ticketing.ticket_types SET reserved_quantity=1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO billing.payments(order_id,provider_id,provider_transaction_id,amount,currency,status,payment_method) VALUES ($1,$2,$3,10,'MXN','processing','card')`, orderID, providerID, fmt.Sprintf("pi_race_%d", id)); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewTicketTypeRepository(pool)
	count, err := repo.ReleaseExpiredReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var reserved int
	var status string
	if err := pool.QueryRow(ctx, `SELECT tt.reserved_quantity,t.status FROM ticketing.ticket_types tt JOIN ticketing.tickets t ON t.ticket_type_id=tt.id WHERE tt.id=$1`, id).Scan(&reserved, &status); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 || status != "reserved" {
		t.Fatalf("in-flight payment was released: count=%d inventory=%d status=%s", count, reserved, status)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing.payments SET status='failed' WHERE order_id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReleaseExpiredReservations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT tt.reserved_quantity,t.status FROM ticketing.ticket_types tt JOIN ticketing.tickets t ON t.ticket_type_id=tt.id WHERE tt.id=$1`, id).Scan(&reserved, &status); err != nil {
		t.Fatal(err)
	}
	if reserved != 1 || status != "reserved" {
		t.Fatalf("retriable failed payment was released: inventory=%d status=%s", reserved, status)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing.payments SET cancelled_at=NOW() WHERE order_id=$1 AND status='failed'`, orderID); err != nil {
		t.Fatal(err)
	}
	count, err = repo.ReleaseExpiredReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT tt.reserved_quantity,t.status FROM ticketing.ticket_types tt JOIN ticketing.tickets t ON t.ticket_type_id=tt.id WHERE tt.id=$1`, id).Scan(&reserved, &status); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 || status != "expired" {
		t.Fatalf("failed payment inventory not released: count=%d inventory=%d status=%s", count, reserved, status)
	}
}

func TestIntentCreationAndExpiryRaceIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ensureStripeTestProvider(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	typeID := concurrencyTicketType(t, pool, 1)
	var eventID, orderID int64
	var orderPublicID string
	if err := pool.QueryRow(ctx, `SELECT event_id FROM ticketing.ticket_types WHERE id=$1`, typeID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.orders(customer_email,customer_name,subtotal,tax_amount,service_fee_amount,discount_amount,total_amount,currency,status,payment_status) VALUES ($1,'Test Buyer',10,0,0,0,10,'MXN','pending','pending') RETURNING id,public_uuid`, fmt.Sprintf("race-%d@example.invalid", typeID)).Scan(&orderID, &orderPublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,reserved_at,reservation_expires_at) VALUES ($1,$2,$3,$4,'test-only','reserved',10,'MXN',NOW(),NOW()+INTERVAL '5 minutes')`, typeID, eventID, orderID, fmt.Sprintf("RACE-INTENT-%d", typeID)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ticketing.ticket_types SET reserved_quantity=1 WHERE id=$1`, typeID); err != nil {
		t.Fatal(err)
	}
	provider := &raceStripeClient{
		started: make(chan struct{}), release: make(chan struct{}),
		intent: &stripe.PaymentIntent{ID: fmt.Sprintf("pi_race_%d", typeID), ClientSecret: "test_client_secret", Amount: 1000, Currency: stripe.Currency("mxn")},
	}
	service := NewPaymentService(
		postgres.NewPaymentRepository(pool), postgres.NewOrderRepository(pool),
		postgres.NewTicketRepository(pool), postgres.NewTicketTypeRepository(pool),
		nil, provider, "", nil,
	)
	type intentResult struct {
		id  string
		err error
	}
	results := make(chan intentResult, 2)
	create := func() {
		res, err := service.CreatePaymentIntent(ctx, &paymentdto.CreatePaymentIntentRequest{OrderID: orderPublicID, Currency: "MXN"})
		if err != nil {
			results <- intentResult{err: err}
			return
		}
		results <- intentResult{id: res.PaymentIntentID}
	}
	go create()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("Stripe creation did not start")
	}
	// Advance only this fixture's deadline while the first request holds the
	// order lock. A second request and expiry race for the same order.
	if _, err := pool.Exec(ctx, `UPDATE ticketing.tickets SET reservation_expires_at=NOW()-INTERVAL '1 second' WHERE order_id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	go create()
	expiryResult := make(chan intentResult, 1)
	go func() {
		n, err := postgres.NewTicketTypeRepository(pool).ReleaseExpiredReservations(ctx)
		expiryResult <- intentResult{id: fmt.Sprint(n), err: err}
	}()
	close(provider.release)
	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil || res.id != provider.intent.ID {
			t.Fatalf("payment intent race: id=%q error=%v", res.id, res.err)
		}
	}
	expired := <-expiryResult
	if expired.err != nil {
		t.Fatalf("expiry released active payment: count=%s error=%v", expired.id, expired.err)
	}
	provider.mu.Lock()
	creates := provider.creates
	provider.mu.Unlock()
	var payments, reserved, inventory int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing.payments WHERE order_id=$1 AND status='processing'), (SELECT count(*) FROM ticketing.tickets WHERE order_id=$1 AND status='reserved'), (SELECT reserved_quantity FROM ticketing.ticket_types WHERE id=$2)`, orderID, typeID).Scan(&payments, &reserved, &inventory); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || payments != 1 || reserved != 1 || inventory != 1 {
		t.Fatalf("intent race mismatch: creates=%d payments=%d reserved=%d inventory=%d", creates, payments, reserved, inventory)
	}
}
