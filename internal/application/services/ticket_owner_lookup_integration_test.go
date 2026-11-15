package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
)

func TestCustomerLookupUsesAuthenticatedPublicIDIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	var userID, customerID int64
	var publicID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO auth.users(email,password_hash,is_active)
		VALUES ($1,'test-only-hash',true) RETURNING id,public_uuid::text
	`, fmt.Sprintf("email-owner-%d@example.invalid", suffix)).Scan(&userID, &publicID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = pool.Exec(cleanup, `DELETE FROM crm.customers WHERE id=$1`, customerID)
		_, _ = pool.Exec(cleanup, `DELETE FROM auth.users WHERE id=$1`, userID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO crm.customers(user_id,full_name,email)
		VALUES ($1,'Test Owner',$2) RETURNING id
	`, userID, fmt.Sprintf("email-owner-%d@example.invalid", suffix)).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	service := NewTicketService(postgres.NewTicketRepository(pool), nil, nil, nil, nil, nil)
	actual, err := service.GetCustomerIDByUserID(ctx, publicID)
	if err != nil || actual != customerID {
		t.Fatalf("authenticated owner lookup failed: match=%v err=%v", actual == customerID, err)
	}
	if _, err := service.GetCustomerIDByUserID(ctx, "not-a-uuid"); err == nil {
		t.Fatal("invalid public ID resolved a customer")
	}
	if _, err := service.GetCustomerIDByUserID(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrCustomerProfileNotFound) {
		t.Fatalf("unrelated user resolved a customer: %v", err)
	}
}
