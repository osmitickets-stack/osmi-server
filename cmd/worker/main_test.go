package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/stripe/stripe-go/v83"
)

type fakeIntentCanceler struct {
	status string
}

func (f *fakeIntentCanceler) CancelPaymentIntent(string) (*stripe.PaymentIntent, error) {
	if f.status != "canceled" {
		return nil, errors.New("cannot cancel")
	}
	return &stripe.PaymentIntent{Status: stripe.PaymentIntentStatus("canceled")}, nil
}

func (f *fakeIntentCanceler) GetPaymentIntent(string) (*stripe.PaymentIntent, error) {
	return &stripe.PaymentIntent{Status: stripe.PaymentIntentStatus(f.status)}, nil
}

func workerTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OSMI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OSMI_TEST_DATABASE_URL is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() != "55439" ||
		u.Path != "/osmidb_test" || u.User.Username() != "osmi" {
		t.Fatal("integration database is not the isolated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("cannot connect to isolated test database")
	}
	t.Cleanup(pool.Close)
	var name, user string
	var marker, dirty bool
	var version int
	err = pool.QueryRow(ctx, `
		SELECT current_database(), current_user,
		  EXISTS(SELECT 1 FROM public.osmi_checkin_test_guard WHERE marker='osmi-checkin-isolated-v1'),
		  version, dirty FROM public.schema_migrations
	`).Scan(&name, &user, &marker, &version, &dirty)
	if err != nil || name != "osmidb_test" || user != "osmi" || !marker || version != 13 || dirty {
		t.Fatal("isolated database guard failed")
	}
	return pool
}

func workerFixture(t *testing.T, pool *pgxpool.Pool) (int64, int64, int64, string) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	var organizerID, eventID, typeID, orderID, paymentID int64
	var providerID int16
	providerCode := fmt.Sprintf("w%016x", uint64(suffix))
	intentID := fmt.Sprintf("pi_worker_%d", suffix)
	email := fmt.Sprintf("worker-%d@example.invalid", suffix)
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.organizers(name,slug,contact_email) VALUES ('Worker Test',$1,'integration@example.invalid') RETURNING id`, fmt.Sprintf("worker-org-%d", suffix)).Scan(&organizerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.events(organizer_id,slug,name,starts_at,ends_at) VALUES ($1,$2,'Worker Event',NOW()+INTERVAL '1 day',NOW()+INTERVAL '1 day 2 hours') RETURNING id`, organizerID, fmt.Sprintf("worker-event-%d", suffix)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ticketing.ticket_types(event_id,name,base_price,currency,total_quantity,reserved_quantity,sold_quantity) VALUES ($1,'Worker Ticket',10,'MXN',1,1,0) RETURNING id`, eventID).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.orders(customer_email,customer_name,subtotal,tax_amount,service_fee_amount,discount_amount,total_amount,currency,status,payment_status) VALUES ($1,'Test Buyer',10,0,0,0,10,'MXN','pending','pending') RETURNING id`, email).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,reserved_at,reservation_expires_at) VALUES ($1,$2,$3,$4,'test-only','reserved',10,'MXN',NOW()-INTERVAL '20 minutes',NOW()-INTERVAL '5 minutes')`, typeID, eventID, orderID, fmt.Sprintf("WORKER-%d", suffix)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.payment_providers(code,name) VALUES ($1,'Worker Test') RETURNING id`, providerCode).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO billing.payments(order_id,provider_id,provider_transaction_id,amount,currency,status) VALUES ($1,$2,$3,10,'MXN','processing') RETURNING id`, orderID, providerID, intentID).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.tickets WHERE order_id=$1`, orderID)
		_, _ = pool.Exec(cleanup, `DELETE FROM billing.payments WHERE order_id=$1`, orderID)
		_, _ = pool.Exec(cleanup, `DELETE FROM billing.orders WHERE id=$1`, orderID)
		_, _ = pool.Exec(cleanup, `DELETE FROM billing.payment_providers WHERE id=$1`, providerID)
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.ticket_types WHERE id=$1`, typeID)
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.events WHERE id=$1`, eventID)
		_, _ = pool.Exec(cleanup, `DELETE FROM ticketing.organizers WHERE id=$1`, organizerID)
	})
	return orderID, typeID, paymentID, intentID
}

func TestWorkerCancelsBeforeReleasingInventoryIntegration(t *testing.T) {
	pool := workerTestDB(t)
	for _, tc := range []struct {
		name, providerStatus, ticketStatus, paymentStatus string
		reserved                                          int
	}{
		{"canceled", "canceled", "expired", "failed", 0},
		{"processing", "processing", "reserved", "processing", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			orderID, typeID, paymentID, intentID := workerFixture(t, pool)
			confirmed, err := reconcileOneExpiredIntent(ctx, pool, &fakeIntentCanceler{status: tc.providerStatus}, expiredIntentCandidate{paymentID: paymentID, orderID: orderID, intentID: intentID})
			if err != nil {
				t.Fatal(err)
			}
			if confirmed != (tc.providerStatus == "canceled") {
				t.Fatalf("unexpected cancellation result: %v", confirmed)
			}
			released, err := postgres.NewTicketTypeRepository(pool).ReleaseExpiredReservations(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var paymentStatus, ticketStatus string
			var reserved int
			if err := pool.QueryRow(ctx, `SELECT p.status,t.status,tt.reserved_quantity FROM billing.payments p JOIN ticketing.tickets t ON t.order_id=p.order_id JOIN ticketing.ticket_types tt ON tt.id=t.ticket_type_id WHERE p.order_id=$1 AND tt.id=$2`, orderID, typeID).Scan(&paymentStatus, &ticketStatus, &reserved); err != nil {
				t.Fatal(err)
			}
			if paymentStatus != tc.paymentStatus || ticketStatus != tc.ticketStatus || reserved != tc.reserved {
				t.Fatalf("worker state mismatch: released=%d payment=%s ticket=%s reserved=%d", released, paymentStatus, ticketStatus, reserved)
			}
		})
	}
}
