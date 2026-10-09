package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

func TestVerifyTicketCredentialIntegration(t *testing.T) {
	pool := requireIntegrationDB(t)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()

	// ========================================================
	// CREDENTIAL SERVICE
	// ========================================================

	credentialService, err := security.NewTicketCredentialService(
		"k1",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf(
			"create ticket credential service: %v",
			err,
		)
	}

	ticketRepo := postgres.NewTicketRepository(pool)

	service := NewTicketService(
		ticketRepo,
		nil,
		nil,
		nil,
		nil,
		credentialService,
	)

	// ========================================================
	// UNIQUE FIXTURE
	// ========================================================

	suffix := time.Now().UnixNano()

	organizerSlug := fmt.Sprintf(
		"credential-integration-organizer-%d",
		suffix,
	)

	eventSlug := fmt.Sprintf(
		"credential-integration-event-%d",
		suffix,
	)

	// --------------------------------------------------------
	// ORGANIZER
	// --------------------------------------------------------

	var organizerID int64

	err = pool.QueryRow(
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
		"Credential Integration Organizer",
		organizerSlug,
		"credential-integration@example.com",
	).Scan(&organizerID)
	if err != nil {
		t.Fatalf(
			"create organizer fixture: %v",
			err,
		)
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
		"Credential Integration Event",
	).Scan(&eventID)
	if err != nil {
		t.Fatalf(
			"create event fixture: %v",
			err,
		)
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
		VALUES (
			$1,
			$2,
			10.00,
			'MXN',
			10,
			1,
			4
		)
		RETURNING id
		`,
		eventID,
		fmt.Sprintf(
			"Credential Integration Ticket %d",
			suffix,
		),
	).Scan(&ticketTypeID)
	if err != nil {
		t.Fatalf(
			"create ticket type fixture: %v",
			err,
		)
	}

	// ========================================================
	// CLEANUP
	// ========================================================

	var createdTicketIDs []int64

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()

		for _, ticketID := range createdTicketIDs {
			_, _ = pool.Exec(
				cleanupCtx,
				`
				DELETE FROM ticketing.tickets
				WHERE id = $1
				`,
				ticketID,
			)
		}

		_, _ = pool.Exec(
			cleanupCtx,
			`
			DELETE FROM ticketing.ticket_types
			WHERE id = $1
			`,
			ticketTypeID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`
			DELETE FROM ticketing.events
			WHERE id = $1
			`,
			eventID,
		)

		_, _ = pool.Exec(
			cleanupCtx,
			`
			DELETE FROM ticketing.organizers
			WHERE id = $1
			`,
			organizerID,
		)
	})

	// ========================================================
	// HELPER: CREATE SIGNED TICKET
	// ========================================================

	createTicket := func(
		t *testing.T,
		status string,
	) (
		int64,
		string,
		string,
	) {
		t.Helper()

		publicID := uuid.New().String()

		credential, err := credentialService.Sign(
			publicID,
		)
		if err != nil {
			t.Fatalf(
				"sign %s ticket credential: %v",
				status,
				err,
			)
		}

		code := fmt.Sprintf(
			"CRED-%s-%s",
			strings.ToUpper(status),
			uuid.New().String()[:8],
		)

		var ticketID int64

		switch status {
		case "checked_in":
			err = pool.QueryRow(
				ctx,
				`
				INSERT INTO ticketing.tickets (
					public_uuid,
					ticket_type_id,
					event_id,
					code,
					secret_hash,
					qr_code_data,
					status,
					final_price,
					currency,
					sold_at,
					checked_in_at,
					created_at,
					updated_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					$6,
					'checked_in',
					10.00,
					'MXN',
					NOW(),
					NOW(),
					NOW(),
					NOW()
				)
				RETURNING id
				`,
				publicID,
				ticketTypeID,
				eventID,
				code,
				uuid.New().String(),
				credential,
			).Scan(&ticketID)

		case "reserved":
			err = pool.QueryRow(
				ctx,
				`
				INSERT INTO ticketing.tickets (
					public_uuid,
					ticket_type_id,
					event_id,
					code,
					secret_hash,
					qr_code_data,
					status,
					final_price,
					currency,
					reserved_at,
					reservation_expires_at,
					created_at,
					updated_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					$6,
					'reserved',
					10.00,
					'MXN',
					NOW(),
					NOW() + INTERVAL '15 minutes',
					NOW(),
					NOW()
				)
				RETURNING id
				`,
				publicID,
				ticketTypeID,
				eventID,
				code,
				uuid.New().String(),
				credential,
			).Scan(&ticketID)

		case "refunded":
			err = pool.QueryRow(
				ctx,
				`
				INSERT INTO ticketing.tickets (
					public_uuid,
					ticket_type_id,
					event_id,
					code,
					secret_hash,
					qr_code_data,
					status,
					final_price,
					currency,
					sold_at,
					refunded_at,
					created_at,
					updated_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					$6,
					'refunded',
					10.00,
					'MXN',
					NOW(),
					NOW(),
					NOW(),
					NOW()
				)
				RETURNING id
				`,
				publicID,
				ticketTypeID,
				eventID,
				code,
				uuid.New().String(),
				credential,
			).Scan(&ticketID)

		case "cancelled":
			err = pool.QueryRow(
				ctx,
				`
				INSERT INTO ticketing.tickets (
					public_uuid,
					ticket_type_id,
					event_id,
					code,
					secret_hash,
					qr_code_data,
					status,
					final_price,
					currency,
					cancelled_at,
					created_at,
					updated_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					$6,
					'cancelled',
					10.00,
					'MXN',
					NOW(),
					NOW(),
					NOW()
				)
				RETURNING id
				`,
				publicID,
				ticketTypeID,
				eventID,
				code,
				uuid.New().String(),
				credential,
			).Scan(&ticketID)

		default:
			err = pool.QueryRow(
				ctx,
				`
				INSERT INTO ticketing.tickets (
					public_uuid,
					ticket_type_id,
					event_id,
					code,
					secret_hash,
					qr_code_data,
					status,
					final_price,
					currency,
					sold_at,
					created_at,
					updated_at
				)
				VALUES (
					$1,
					$2,
					$3,
					$4,
					$5,
					$6,
					$7,
					10.00,
					'MXN',
					NOW(),
					NOW(),
					NOW()
				)
				RETURNING id
				`,
				publicID,
				ticketTypeID,
				eventID,
				code,
				uuid.New().String(),
				credential,
				status,
			).Scan(&ticketID)
		}

		if err != nil {
			t.Fatalf(
				"create %s ticket fixture: %v",
				status,
				err,
			)
		}

		createdTicketIDs = append(
			createdTicketIDs,
			ticketID,
		)

		return ticketID, publicID, credential
	}

	// ========================================================
	// FIXTURES
	// ========================================================

	soldTicketID, soldPublicID, soldCredential :=
		createTicket(t, "sold")

	_, checkedPublicID, checkedCredential :=
		createTicket(t, "checked_in")

	_, reservedPublicID, reservedCredential :=
		createTicket(t, "reserved")

	_, refundedPublicID, refundedCredential :=
		createTicket(t, "refunded")

	_, cancelledPublicID, cancelledCredential :=
		createTicket(t, "cancelled")

	// ========================================================
	// SNAPSHOT BEFORE VALIDATION
	// ========================================================

	var beforeStatus string
	var beforeCheckedInAt *time.Time
	var beforeLastValidatedAt *time.Time
	var beforeValidationCount int
	var beforeUpdatedAt time.Time

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			status,
			checked_in_at,
			last_validated_at,
			COALESCE(validation_count, 0),
			updated_at
		FROM ticketing.tickets
		WHERE id = $1
		`,
		soldTicketID,
	).Scan(
		&beforeStatus,
		&beforeCheckedInAt,
		&beforeLastValidatedAt,
		&beforeValidationCount,
		&beforeUpdatedAt,
	)
	if err != nil {
		t.Fatalf(
			"read sold ticket before validation: %v",
			err,
		)
	}

	// ========================================================
	// SOLD → VALID
	// ========================================================

	soldResult, err := service.VerifyTicketCredential(
		ctx,
		soldCredential,
	)
	if err != nil {
		t.Fatalf(
			"verify sold credential: %v",
			err,
		)
	}

	if !soldResult.Authentic {
		t.Fatal(
			"sold credential must be authentic",
		)
	}

	if !soldResult.CanCheckIn {
		t.Fatal(
			"sold ticket must be eligible for check-in",
		)
	}

	if soldResult.Result != "VALID" {
		t.Fatalf(
			"expected VALID, got %q",
			soldResult.Result,
		)
	}

	if soldResult.TicketPublicID != soldPublicID {
		t.Fatalf(
			"expected sold public id %q, got %q",
			soldPublicID,
			soldResult.TicketPublicID,
		)
	}

	// ========================================================
	// CHECKED_IN → ALREADY_CHECKED_IN
	// ========================================================

	checkedResult, err := service.VerifyTicketCredential(
		ctx,
		checkedCredential,
	)
	if err != nil {
		t.Fatalf(
			"verify checked-in credential: %v",
			err,
		)
	}

	if checkedResult.TicketPublicID != checkedPublicID {
		t.Fatalf(
			"unexpected checked-in ticket id",
		)
	}

	if !checkedResult.Authentic {
		t.Fatal(
			"checked-in credential must remain authentic",
		)
	}

	if checkedResult.CanCheckIn {
		t.Fatal(
			"checked-in ticket must not be eligible",
		)
	}

	if checkedResult.Result != "ALREADY_CHECKED_IN" {
		t.Fatalf(
			"expected ALREADY_CHECKED_IN, got %q",
			checkedResult.Result,
		)
	}

	if checkedResult.CheckedInAt == nil {
		t.Fatal(
			"checked-in ticket must expose checked_in_at",
		)
	}

	// ========================================================
	// RESERVED → NOT_PAID
	// ========================================================

	reservedResult, err := service.VerifyTicketCredential(
		ctx,
		reservedCredential,
	)
	if err != nil {
		t.Fatalf(
			"verify reserved credential: %v",
			err,
		)
	}

	if reservedResult.TicketPublicID != reservedPublicID ||
		reservedResult.Result != "NOT_PAID" ||
		reservedResult.CanCheckIn {
		t.Fatalf(
			"unexpected reserved result: %+v",
			reservedResult,
		)
	}

	// ========================================================
	// REFUNDED → REFUNDED
	// ========================================================

	refundedResult, err := service.VerifyTicketCredential(
		ctx,
		refundedCredential,
	)
	if err != nil {
		t.Fatalf(
			"verify refunded credential: %v",
			err,
		)
	}

	if refundedResult.TicketPublicID != refundedPublicID ||
		refundedResult.Result != "REFUNDED" ||
		refundedResult.CanCheckIn {
		t.Fatalf(
			"unexpected refunded result: %+v",
			refundedResult,
		)
	}

	// ========================================================
	// CANCELLED → CANCELLED
	// ========================================================

	cancelledResult, err := service.VerifyTicketCredential(
		ctx,
		cancelledCredential,
	)
	if err != nil {
		t.Fatalf(
			"verify cancelled credential: %v",
			err,
		)
	}

	if cancelledResult.TicketPublicID != cancelledPublicID ||
		cancelledResult.Result != "CANCELLED" ||
		cancelledResult.CanCheckIn {
		t.Fatalf(
			"unexpected cancelled result: %+v",
			cancelledResult,
		)
	}

	// ========================================================
	// TAMPERED SIGNATURE → INVALID
	// ========================================================

	parts := strings.Split(
		soldCredential,
		".",
	)

	if len(parts) != 4 {
		t.Fatalf(
			"unexpected credential format: %q",
			soldCredential,
		)
	}

	if strings.HasSuffix(parts[3], "A") {
		parts[3] =
			parts[3][:len(parts[3])-1] + "B"
	} else {
		parts[3] =
			parts[3][:len(parts[3])-1] + "A"
	}

	tamperedCredential := strings.Join(
		parts,
		".",
	)

	_, err = service.VerifyTicketCredential(
		ctx,
		tamperedCredential,
	)

	if !errors.Is(
		err,
		ErrTicketCredentialInvalid,
	) {
		t.Fatalf(
			"expected invalid credential error, got %v",
			err,
		)
	}

	// ========================================================
	// AUTHENTIC CREDENTIAL + NONEXISTENT UUID → NOT FOUND
	// ========================================================

	missingPublicID := uuid.New().String()

	missingCredential, err := credentialService.Sign(
		missingPublicID,
	)
	if err != nil {
		t.Fatalf(
			"sign missing credential: %v",
			err,
		)
	}

	_, err = service.VerifyTicketCredential(
		ctx,
		missingCredential,
	)

	if !errors.Is(
		err,
		ErrTicketCredentialTicketNotFound,
	) {
		t.Fatalf(
			"expected ticket not found error, got %v",
			err,
		)
	}

	// ========================================================
	// VERIFY MUST BE NON-DESTRUCTIVE
	// ========================================================

	var afterStatus string
	var afterCheckedInAt *time.Time
	var afterLastValidatedAt *time.Time
	var afterValidationCount int
	var afterUpdatedAt time.Time

	err = pool.QueryRow(
		ctx,
		`
		SELECT
			status,
			checked_in_at,
			last_validated_at,
			COALESCE(validation_count, 0),
			updated_at
		FROM ticketing.tickets
		WHERE id = $1
		`,
		soldTicketID,
	).Scan(
		&afterStatus,
		&afterCheckedInAt,
		&afterLastValidatedAt,
		&afterValidationCount,
		&afterUpdatedAt,
	)
	if err != nil {
		t.Fatalf(
			"read sold ticket after validation: %v",
			err,
		)
	}

	if afterStatus != beforeStatus {
		t.Fatalf(
			"validation mutated status: before=%q after=%q",
			beforeStatus,
			afterStatus,
		)
	}

	if afterCheckedInAt != nil ||
		beforeCheckedInAt != nil {
		t.Fatal(
			"validation must not set checked_in_at",
		)
	}

	if afterValidationCount != beforeValidationCount {
		t.Fatalf(
			"validation mutated validation_count: before=%d after=%d",
			beforeValidationCount,
			afterValidationCount,
		)
	}

	if !sameOptionalTime(
		beforeLastValidatedAt,
		afterLastValidatedAt,
	) {
		t.Fatal(
			"validation mutated last_validated_at",
		)
	}

	if !afterUpdatedAt.Equal(beforeUpdatedAt) {
		t.Fatalf(
			"validation performed an unexpected database update: before=%s after=%s",
			beforeUpdatedAt,
			afterUpdatedAt,
		)
	}

	t.Logf(
		"ticket credential validation passed: sold=%s checked=%s reserved=%s refunded=%s cancelled=%s",
		soldResult.Result,
		checkedResult.Result,
		reservedResult.Result,
		refundedResult.Result,
		cancelledResult.Result,
	)
}

func sameOptionalTime(
	a *time.Time,
	b *time.Time,
) bool {
	if a == nil && b == nil {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	return a.Equal(*b)
}
