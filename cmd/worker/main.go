// cmd/worker/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/database"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/payment"
	postgres "github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/stripe/stripe-go/v83"
)

const (
	workerInterval = 5 * time.Minute
	queryTimeout   = 2 * time.Minute
)

type intentCanceler interface {
	CancelPaymentIntent(string) (*stripe.PaymentIntent, error)
	GetPaymentIntent(string) (*stripe.PaymentIntent, error)
}

type expiredIntentCandidate struct {
	paymentID int64
	orderID   int64
	intentID  string
}

func main() {
	stripeKey := os.Getenv("STRIPE_SECRET_KEY")
	if stripeKey == "" {
		log.Fatal("reservation worker requires STRIPE_SECRET_KEY")
	}
	if err := database.Init(); err != nil {
		log.Fatalf("failed to initialize database connection: %v", err)
	}
	defer database.Close()
	provider := payment.NewStripeClient(stripeKey)
	stripeKey = ""
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		canceled, err := reconcileExpiredIntents(ctx, database.Pool, provider)
		if err != nil {
			log.Printf("reservation reconciliation failed: %v", err)
			return
		}
		released, err := postgres.NewTicketTypeRepository(database.Pool).ReleaseExpiredReservations(ctx)
		if err != nil {
			log.Printf("reservation expiry failed: %v", err)
			return
		}
		log.Printf("reservation maintenance completed: canceled_intents=%d released_tickets=%d", canceled, released)
	}
	run()
	ticker := time.NewTicker(workerInterval)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}

// A failed card attempt is reusable; inventory can be released only after the
// provider confirms cancellation. A second worker may see the same intent and
// is safe because both the provider state and database update are idempotent.
func reconcileExpiredIntents(ctx context.Context, pool *pgxpool.Pool, provider intentCanceler) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id, p.order_id, p.provider_transaction_id
		FROM billing.payments p
		WHERE p.status IN ('processing','failed') AND p.cancelled_at IS NULL
		  AND p.provider_transaction_id IS NOT NULL
		  AND EXISTS (
		    SELECT 1 FROM ticketing.tickets t
		    WHERE t.order_id=p.order_id AND t.status='reserved'
		      AND t.reservation_expires_at < NOW()
		  )
		ORDER BY p.id LIMIT 10
	`)
	if err != nil {
		return 0, fmt.Errorf("query expired payment intents: %w", err)
	}
	var candidates []expiredIntentCandidate
	for rows.Next() {
		var c expiredIntentCandidate
		if err := rows.Scan(&c.paymentID, &c.orderID, &c.intentID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired payment intent: %w", err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("read expired payment intents: %w", err)
	}

	canceled := 0
	for _, c := range candidates {
		confirmed, err := reconcileOneExpiredIntent(ctx, pool, provider, c)
		if err != nil {
			return canceled, err
		}
		if confirmed {
			canceled++
		}
	}
	return canceled, nil
}

func reconcileOneExpiredIntent(ctx context.Context, pool *pgxpool.Pool, provider intentCanceler, c expiredIntentCandidate) (bool, error) {
	pi, err := provider.CancelPaymentIntent(c.intentID)
	if err != nil {
		// A retry can find an already canceled intent; inspect provider state.
		pi, err = provider.GetPaymentIntent(c.intentID)
	}
	if err != nil || pi == nil || string(pi.Status) != "canceled" {
		return false, nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin canceled intent transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM billing.orders WHERE id=$1 FOR UPDATE`, c.orderID).Scan(&locked); err != nil {
		return false, fmt.Errorf("lock canceled intent order: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE billing.payments
		SET status='failed', cancelled_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND order_id=$2 AND provider_transaction_id=$3
		  AND status IN ('processing','failed') AND cancelled_at IS NULL
	`, c.paymentID, c.orderID, c.intentID); err != nil {
		return false, fmt.Errorf("record canceled intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit canceled intent: %w", err)
	}
	return true, nil
}
