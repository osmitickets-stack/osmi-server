package maintenance

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

func TestReissueLegacyQRIntegration(t *testing.T) {
	url := os.Getenv("OSMI_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("OSMI_TEST_DATABASE_URL is not configured")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid isolated database URL")
	}
	if config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 55439 || config.ConnConfig.User != "osmi" || config.ConnConfig.Database != "osmidb_test" {
		t.Fatal("refusing to run outside isolated PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("connect isolated PostgreSQL")
	}
	t.Cleanup(pool.Close)
	var marker string
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, `SELECT marker, (SELECT version FROM public.schema_migrations), (SELECT dirty FROM public.schema_migrations) FROM public.osmi_checkin_test_guard`).Scan(&marker, &version, &dirty); err != nil || marker != "osmi-checkin-isolated-v1" || version != 13 || dirty {
		t.Fatal("isolated PostgreSQL marker or schema invalid")
	}

	suffix := uuid.NewString()
	fixtureTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin isolated fixture")
	}
	defer fixtureTx.Rollback(ctx)
	var organizerID, eventID, ticketTypeID, orderID, ticketID int64
	var providerID int16
	var ticketPublicID string
	create := func(query string, args []any, scan ...any) {
		t.Helper()
		if err := fixtureTx.QueryRow(ctx, query, args...).Scan(scan...); err != nil {
			t.Fatalf("create isolated fixture: %v", err)
		}
	}
	create(`INSERT INTO ticketing.organizers(name,slug,contact_email) VALUES ('QR reissue test',$1,'qr-test@example.invalid') RETURNING id`, []any{"qr-reissue-" + suffix}, &organizerID)
	create(`INSERT INTO ticketing.events(organizer_id,slug,name,starts_at,ends_at) VALUES ($1,$2,'QR reissue test',now()+interval '1 day',now()+interval '1 day 2 hours') RETURNING id`, []any{organizerID, "qr-reissue-" + suffix}, &eventID)
	create(`INSERT INTO ticketing.ticket_types(event_id,name,base_price,currency,total_quantity,sold_quantity) VALUES ($1,'QR reissue test',10,'MXN',1,1) RETURNING id`, []any{eventID}, &ticketTypeID)
	create(`INSERT INTO billing.payment_providers(code,name) VALUES ($1,'QR reissue test') RETURNING id`, []any{"qr" + suffix[:14]}, &providerID)
	create(`INSERT INTO billing.orders(customer_email,subtotal,total_amount,currency,status,payment_status,paid_at) VALUES ('qr-test@example.invalid',10,10,'MXN','completed','paid',now()) RETURNING id`, nil, &orderID)
	create(`INSERT INTO billing.payments(order_id,provider_id,amount,currency,status) VALUES ($1,$2,10,'MXN','completed') RETURNING id`, []any{orderID, providerID}, new(int64))
	create(`INSERT INTO ticketing.tickets(ticket_type_id,event_id,order_id,code,secret_hash,status,final_price,currency,sold_at) VALUES ($1,$2,$3,$4,'isolated-test','sold',10,'MXN',now()) RETURNING id,public_uuid::text`, []any{ticketTypeID, eventID, orderID, "QR-" + suffix}, &ticketID, &ticketPublicID)
	if err := fixtureTx.Commit(ctx); err != nil {
		t.Fatal("commit isolated fixture")
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, q := range []struct {
			sql string
			id  any
		}{
			{`DELETE FROM ticketing.tickets WHERE id=$1`, ticketID},
			{`DELETE FROM billing.payments WHERE order_id=$1`, orderID},
			{`DELETE FROM billing.orders WHERE id=$1`, orderID},
			{`DELETE FROM ticketing.ticket_types WHERE id=$1`, ticketTypeID},
			{`DELETE FROM ticketing.events WHERE id=$1`, eventID},
			{`DELETE FROM ticketing.organizers WHERE id=$1`, organizerID},
			{`DELETE FROM billing.payment_providers WHERE id=$1`, providerID},
		} {
			if _, err := pool.Exec(cleanupCtx, q.sql, q.id); err != nil {
				t.Errorf("cleanup isolated fixture: %v", err)
			}
		}
	})

	signer, err := security.NewTicketCredentialService("test-k1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReissueLegacyQR(ctx, pool, signer, "wrong_database", true); err == nil {
		t.Fatal("wrong database was accepted")
	}
	count, err := ReissueLegacyQR(ctx, pool, signer, "osmidb_test", false)
	if err != nil || count != 1 {
		t.Fatalf("dry run failed: count=%d err=%v", count, err)
	}
	var before *string
	if err := pool.QueryRow(ctx, `SELECT qr_code_data FROM ticketing.tickets WHERE id=$1`, ticketID).Scan(&before); err != nil || before != nil {
		t.Fatal("dry run changed QR")
	}
	count, err = ReissueLegacyQR(ctx, pool, signer, "osmidb_test", true)
	if err != nil || count != 1 {
		t.Fatalf("apply failed: count=%d err=%v", count, err)
	}
	var credential string
	if err := pool.QueryRow(ctx, `SELECT qr_code_data FROM ticketing.tickets WHERE id=$1`, ticketID).Scan(&credential); err != nil {
		t.Fatal("signed credential missing")
	}
	verifiedID, err := signer.Verify(credential)
	if err != nil || verifiedID != ticketPublicID {
		t.Fatal("stored credential did not verify")
	}
	count, err = ReissueLegacyQR(ctx, pool, signer, "osmidb_test", true)
	if !errors.Is(err, ErrUnsafeLegacyTicketSelection) || count != 0 {
		t.Fatal("second application did not refuse")
	}
	var status string
	var validations int
	if err := pool.QueryRow(ctx, `SELECT status::text,validation_count FROM ticketing.tickets WHERE id=$1`, ticketID).Scan(&status, &validations); err != nil || status != "sold" || validations != 0 {
		t.Fatal("reissue changed ticket admission state")
	}
}
