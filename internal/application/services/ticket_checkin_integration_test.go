package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
	"github.com/osmitickets-stack/osmi-server/internal/infrastructure/repositories/postgres"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

type checkInFixture struct {
	EventID       int64
	EventPublicID string

	TicketTypeID int64

	TicketID       int64
	TicketPublicID string

	Credential string
}

func newCheckInTestService(
	t *testing.T,
) (*TicketService, *security.TicketCredentialService) {
	t.Helper()

	pool := requireIntegrationDB(t)

	credentialService, err := security.NewTicketCredentialService(
		"test-k1",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatalf("create ticket credential service: %v", err)
	}

	service := NewTicketService(
		postgres.NewTicketRepository(pool),
		postgres.NewTicketTypeRepository(pool),
		postgres.NewEventRepository(pool),
		nil,
		nil,
		credentialService,
	)

	return service, credentialService
}

func createCheckInFixture(
	t *testing.T,
	credentialService *security.TicketCredentialService,
	status string,
	startsAt time.Time,
	endsAt time.Time,
) *checkInFixture {
	t.Helper()

	pool := requireIntegrationDB(t)

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	suffix := time.Now().UnixNano()

	organizerSlug := fmt.Sprintf(
		"checkin-organizer-%d",
		suffix,
	)
	eventSlug := fmt.Sprintf(
		"checkin-event-%d",
		suffix,
	)

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
		"Check-in Integration Organizer",
		organizerSlug,
		fmt.Sprintf(
			"checkin-%d@example.com",
			suffix,
		),
	).Scan(&organizerID)
	if err != nil {
		t.Fatalf("create organizer fixture: %v", err)
	}

	var eventID int64
	var eventPublicID string

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
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, public_uuid::text
		`,
		organizerID,
		eventSlug,
		"Check-in Integration Event",
		startsAt,
		endsAt,
	).Scan(
		&eventID,
		&eventPublicID,
	)
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
			VALUES (
				$1,
				$2,
				10.00,
				'MXN',
				1,
				0,
				1
			)
			RETURNING id
		`,
		eventID,
		"Check-in Integration Ticket",
	).Scan(&ticketTypeID)
	if err != nil {
		t.Fatalf("create ticket type fixture: %v", err)
	}

	ticketPublicID := uuid.New().String()

	credential, err := credentialService.Sign(
		ticketPublicID,
	)
	if err != nil {
		t.Fatalf("sign fixture credential: %v", err)
	}

	ticketCode := fmt.Sprintf(
		"CHK-%d",
		suffix,
	)

	var soldAt interface{}
	var checkedInAt interface{}

	switch status {
	case "sold":
		soldAt = time.Now().Add(-10 * time.Minute)

	case "checked_in":
		soldAt = time.Now().Add(-10 * time.Minute)
		checkedInAt = time.Now().Add(-5 * time.Minute)

	default:
		soldAt = nil
		checkedInAt = nil
	}

	var ticketID int64

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
				validation_count
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
				$8,
				$9,
				0
			)
			RETURNING id
		`,
		ticketPublicID,
		ticketTypeID,
		eventID,
		ticketCode,
		fmt.Sprintf("checkin-secret-%d", suffix),
		credential,
		status,
		soldAt,
		checkedInAt,
	).Scan(&ticketID)
	if err != nil {
		t.Fatalf("create ticket fixture: %v", err)
	}

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

	return &checkInFixture{
		EventID:        eventID,
		EventPublicID:  eventPublicID,
		TicketTypeID:   ticketTypeID,
		TicketID:       ticketID,
		TicketPublicID: ticketPublicID,
		Credential:     credential,
	}
}

func TestCheckInTicketAtomicIntegration(t *testing.T) {
	service, credentialService := newCheckInTestService(t)

	t.Run("valid sold ticket is consumed exactly once", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		result, err := service.CheckInTicket(
			context.Background(),
			&ticketdto.CheckInTicketRequest{
				Credential: fixture.Credential,
				EventID:    fixture.EventPublicID,
				Method:     "qr_code",
				Location:   "main_gate",
			},
		)
		if err != nil {
			t.Fatalf("check in valid ticket: %v", err)
		}

		if !result.Accepted {
			t.Fatalf(
				"expected accepted check-in, got result=%s",
				result.Result,
			)
		}

		if result.Result != "CHECKED_IN" {
			t.Fatalf(
				"expected CHECKED_IN, got %q",
				result.Result,
			)
		}

		if result.Status != "checked_in" {
			t.Fatalf(
				"expected checked_in status, got %q",
				result.Status,
			)
		}

		if result.CheckedInAt == nil {
			t.Fatal("expected checked_in_at")
		}

		pool := requireIntegrationDB(t)

		var status string
		var validationCount int
		var checkedInAt *time.Time
		var lastValidatedAt *time.Time
		var checkedInBy *int64

		err = pool.QueryRow(
			context.Background(),
			`
				SELECT
					status,
					validation_count,
					checked_in_at,
					last_validated_at,
					checked_in_by
				FROM ticketing.tickets
				WHERE id = $1
			`,
			fixture.TicketID,
		).Scan(
			&status,
			&validationCount,
			&checkedInAt,
			&lastValidatedAt,
			&checkedInBy,
		)
		if err != nil {
			t.Fatalf("read checked-in ticket: %v", err)
		}

		if status != "checked_in" {
			t.Fatalf(
				"expected DB status checked_in, got %q",
				status,
			)
		}

		if validationCount != 1 {
			t.Fatalf(
				"expected validation_count=1, got %d",
				validationCount,
			)
		}

		if checkedInAt == nil {
			t.Fatal("expected DB checked_in_at")
		}

		if lastValidatedAt == nil {
			t.Fatal("expected DB last_validated_at")
		}

		if checkedInBy != nil {
			t.Fatalf(
				"expected checked_in_by NULL before 10.4, got %d",
				*checkedInBy,
			)
		}
	})

	t.Run("second scan does not consume twice", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		req := &ticketdto.CheckInTicketRequest{
			Credential: fixture.Credential,
			EventID:    fixture.EventPublicID,
			Method:     "qr_code",
		}

		first, err := service.CheckInTicket(
			context.Background(),
			req,
		)
		if err != nil {
			t.Fatalf("first scan: %v", err)
		}

		if !first.Accepted {
			t.Fatalf(
				"first scan should be accepted: %s",
				first.Result,
			)
		}

		second, err := service.CheckInTicket(
			context.Background(),
			req,
		)
		if err != nil {
			t.Fatalf("second scan: %v", err)
		}

		if second.Accepted {
			t.Fatal("second scan must not be accepted")
		}

		if second.Result != "ALREADY_CHECKED_IN" {
			t.Fatalf(
				"expected ALREADY_CHECKED_IN, got %q",
				second.Result,
			)
		}

		pool := requireIntegrationDB(t)

		var validationCount int

		err = pool.QueryRow(
			context.Background(),
			`
				SELECT validation_count
				FROM ticketing.tickets
				WHERE id = $1
			`,
			fixture.TicketID,
		).Scan(&validationCount)
		if err != nil {
			t.Fatalf("read validation_count: %v", err)
		}

		if validationCount != 1 {
			t.Fatalf(
				"second scan changed validation_count: got %d",
				validationCount,
			)
		}
	})

	t.Run("two concurrent scanners accept exactly one", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		type scanResult struct {
			result *ticketdto.TicketCheckInResponse
			err    error
		}

		start := make(chan struct{})
		results := make(chan scanResult, 2)

		var wg sync.WaitGroup
		wg.Add(2)

		scan := func() {
			defer wg.Done()

			<-start

			result, err := service.CheckInTicket(
				context.Background(),
				&ticketdto.CheckInTicketRequest{
					Credential: fixture.Credential,
					EventID:    fixture.EventPublicID,
					Method:     "qr_code",
				},
			)

			results <- scanResult{
				result: result,
				err:    err,
			}
		}

		go scan()
		go scan()

		close(start)

		wg.Wait()
		close(results)

		accepted := 0
		alreadyCheckedIn := 0

		for scanResult := range results {
			if scanResult.err != nil {
				t.Fatalf(
					"concurrent scan returned error: %v",
					scanResult.err,
				)
			}

			if scanResult.result == nil {
				t.Fatal("concurrent scan returned nil result")
			}

			switch scanResult.result.Result {
			case "CHECKED_IN":
				if !scanResult.result.Accepted {
					t.Fatal(
						"CHECKED_IN result must be accepted",
					)
				}
				accepted++

			case "ALREADY_CHECKED_IN":
				if scanResult.result.Accepted {
					t.Fatal(
						"ALREADY_CHECKED_IN cannot be accepted",
					)
				}
				alreadyCheckedIn++

			default:
				t.Fatalf(
					"unexpected concurrent result %q",
					scanResult.result.Result,
				)
			}
		}

		if accepted != 1 {
			t.Fatalf(
				"expected exactly 1 accepted scan, got %d",
				accepted,
			)
		}

		if alreadyCheckedIn != 1 {
			t.Fatalf(
				"expected exactly 1 ALREADY_CHECKED_IN, got %d",
				alreadyCheckedIn,
			)
		}

		pool := requireIntegrationDB(t)

		var validationCount int

		err := pool.QueryRow(
			context.Background(),
			`
				SELECT validation_count
				FROM ticketing.tickets
				WHERE id = $1
			`,
			fixture.TicketID,
		).Scan(&validationCount)
		if err != nil {
			t.Fatalf(
				"read concurrent validation_count: %v",
				err,
			)
		}

		if validationCount != 1 {
			t.Fatalf(
				"expected validation_count=1 after race, got %d",
				validationCount,
			)
		}
	})

	t.Run("tampered credential is rejected without mutation", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		tampered := fixture.Credential[:len(fixture.Credential)-1] + "A"

		result, err := service.CheckInTicket(
			context.Background(),
			&ticketdto.CheckInTicketRequest{
				Credential: tampered,
				EventID:    fixture.EventPublicID,
			},
		)

		if result != nil {
			t.Fatal("tampered credential must not return a result")
		}

		if !errors.Is(err, ErrTicketCredentialInvalid) {
			t.Fatalf(
				"expected ErrTicketCredentialInvalid, got %v",
				err,
			)
		}

		pool := requireIntegrationDB(t)

		var status string
		var validationCount int
		var checkedInAt *time.Time

		err = pool.QueryRow(
			context.Background(),
			`
				SELECT
					status,
					validation_count,
					checked_in_at
				FROM ticketing.tickets
				WHERE id = $1
			`,
			fixture.TicketID,
		).Scan(
			&status,
			&validationCount,
			&checkedInAt,
		)
		if err != nil {
			t.Fatalf("read tampered fixture: %v", err)
		}

		if status != "sold" ||
			validationCount != 0 ||
			checkedInAt != nil {
			t.Fatalf(
				"tampered scan mutated ticket: status=%s count=%d checked_in_at=%v",
				status,
				validationCount,
				checkedInAt,
			)
		}
	})

	t.Run("wrong event is rejected without mutation", func(t *testing.T) {
		now := time.Now()

		ticketFixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		otherFixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-30*time.Minute),
			now.Add(2*time.Hour),
		)

		result, err := service.CheckInTicket(
			context.Background(),
			&ticketdto.CheckInTicketRequest{
				Credential: ticketFixture.Credential,
				EventID:    otherFixture.EventPublicID,
			},
		)
		if err != nil {
			t.Fatalf("wrong-event scan returned error: %v", err)
		}

		if result.Accepted {
			t.Fatal("wrong-event scan must not be accepted")
		}

		if result.Result != "WRONG_EVENT" {
			t.Fatalf(
				"expected WRONG_EVENT, got %q",
				result.Result,
			)
		}

		pool := requireIntegrationDB(t)

		var status string
		var validationCount int

		err = pool.QueryRow(
			context.Background(),
			`
				SELECT status, validation_count
				FROM ticketing.tickets
				WHERE id = $1
			`,
			ticketFixture.TicketID,
		).Scan(
			&status,
			&validationCount,
		)
		if err != nil {
			t.Fatalf("read wrong-event fixture: %v", err)
		}

		if status != "sold" || validationCount != 0 {
			t.Fatalf(
				"wrong-event scan mutated ticket: status=%s count=%d",
				status,
				validationCount,
			)
		}
	})

	statusCases := []struct {
		name           string
		status         string
		expectedResult string
	}{
		{
			name:           "reserved",
			status:         "reserved",
			expectedResult: "NOT_PAID",
		},
		{
			name:           "refunded",
			status:         "refunded",
			expectedResult: "REFUNDED",
		},
		{
			name:           "cancelled",
			status:         "cancelled",
			expectedResult: "CANCELLED",
		},
		{
			name:           "available",
			status:         "available",
			expectedResult: "NOT_ELIGIBLE",
		},
	}

	for _, tc := range statusCases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()

			fixture := createCheckInFixture(
				t,
				credentialService,
				tc.status,
				now.Add(-30*time.Minute),
				now.Add(2*time.Hour),
			)

			result, err := service.CheckInTicket(
				context.Background(),
				&ticketdto.CheckInTicketRequest{
					Credential: fixture.Credential,
					EventID:    fixture.EventPublicID,
				},
			)
			if err != nil {
				t.Fatalf(
					"%s scan returned error: %v",
					tc.name,
					err,
				)
			}

			if result.Accepted {
				t.Fatalf(
					"%s scan must not be accepted",
					tc.name,
				)
			}

			if result.Result != tc.expectedResult {
				t.Fatalf(
					"expected %s, got %s",
					tc.expectedResult,
					result.Result,
				)
			}
		})
	}

	t.Run("too early", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(3*time.Hour),
			now.Add(5*time.Hour),
		)

		result, err := service.CheckInTicket(
			context.Background(),
			&ticketdto.CheckInTicketRequest{
				Credential: fixture.Credential,
				EventID:    fixture.EventPublicID,
			},
		)
		if err != nil {
			t.Fatalf("too-early scan returned error: %v", err)
		}

		if result.Accepted {
			t.Fatal("too-early scan must not be accepted")
		}

		if result.Result != "TOO_EARLY" {
			t.Fatalf(
				"expected TOO_EARLY, got %q",
				result.Result,
			)
		}
	})

	t.Run("checkin closed", func(t *testing.T) {
		now := time.Now()

		fixture := createCheckInFixture(
			t,
			credentialService,
			"sold",
			now.Add(-6*time.Hour),
			now.Add(-3*time.Hour),
		)

		result, err := service.CheckInTicket(
			context.Background(),
			&ticketdto.CheckInTicketRequest{
				Credential: fixture.Credential,
				EventID:    fixture.EventPublicID,
			},
		)
		if err != nil {
			t.Fatalf("closed-window scan returned error: %v", err)
		}

		if result.Accepted {
			t.Fatal("closed-window scan must not be accepted")
		}

		if result.Result != "CHECKIN_CLOSED" {
			t.Fatalf(
				"expected CHECKIN_CLOSED, got %q",
				result.Result,
			)
		}
	})
}
