package maintenance

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

var ErrUnsafeLegacyTicketSelection = errors.New("expected exactly one paid, sold ticket without a QR credential")

// ReissueLegacyQR signs one legacy paid ticket. It never returns the credential:
// the database update and the checks run in a single transaction.
func ReissueLegacyQR(ctx context.Context, pool *pgxpool.Pool, signer *security.TicketCredentialService, expectedDatabase string, apply bool) (int, error) {
	if signer == nil || expectedDatabase == "" {
		return 0, errors.New("signer and expected database are required")
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, fmt.Errorf("begin reissue transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var database string
	var version int
	var dirty bool
	if err := tx.QueryRow(ctx, `SELECT current_database(), version, dirty FROM public.schema_migrations`).Scan(&database, &version, &dirty); err != nil {
		return 0, fmt.Errorf("read database identity: %w", err)
	}
	if database != expectedDatabase || version != 13 || dirty {
		return 0, errors.New("database identity or migration state does not match")
	}

	// Only a completed, paid order with a completed payment qualifies. Lock the
	// ticket and refuse to proceed if the selection is not unique.
	rows, err := tx.Query(ctx, `
		SELECT t.id, t.public_uuid::text
		FROM ticketing.tickets t
		JOIN billing.orders o ON o.id = t.order_id
		WHERE t.status = 'sold'
		  AND btrim(coalesce(t.qr_code_data, '')) = ''
		  AND t.checked_in_at IS NULL
		  AND t.validation_count = 0
		  AND o.status = 'completed'
		  AND o.payment_status = 'paid'
		  AND EXISTS (
			SELECT 1 FROM billing.payments p
			WHERE p.order_id = o.id AND p.status = 'completed'
		  )
		ORDER BY t.id
		FOR UPDATE OF t
	`)
	if err != nil {
		return 0, fmt.Errorf("select legacy ticket: %w", err)
	}
	type candidate struct {
		id       int64
		publicID string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.publicID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan legacy ticket: %w", err)
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("read legacy tickets: %w", err)
	}
	if len(candidates) != 1 {
		return len(candidates), ErrUnsafeLegacyTicketSelection
	}
	if !apply {
		return 1, nil // Rollback on return: inspection cannot change the ticket.
	}

	credential, err := signer.Sign(candidates[0].publicID)
	if err != nil {
		return 0, fmt.Errorf("sign legacy ticket: %w", err)
	}
	verifiedID, err := signer.Verify(credential)
	if err != nil || verifiedID != candidates[0].publicID {
		return 0, errors.New("credential self-verification failed")
	}

	tag, err := tx.Exec(ctx, `
		UPDATE ticketing.tickets
		SET qr_code_data = $1, updated_at = now()
		WHERE id = $2 AND public_uuid::text = $3
		  AND status = 'sold'
		  AND btrim(coalesce(qr_code_data, '')) = ''
		  AND checked_in_at IS NULL AND validation_count = 0
	`, credential, candidates[0].id, candidates[0].publicID)
	if err != nil {
		return 0, fmt.Errorf("store signed credential: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, errors.New("ticket changed before update")
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit signed credential: %w", err)
	}
	return 1, nil
}
