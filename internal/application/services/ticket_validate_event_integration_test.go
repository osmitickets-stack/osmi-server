package services

import (
	"context"
	"errors"
	"testing"
	"time"

	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
)

func TestValidateEventIntegration(t *testing.T) {
	svc, signer := newCheckInTestService(t)
	now := time.Now()
	a := createCheckInFixture(t, signer, "sold", now.Add(-time.Hour), now.Add(time.Hour))
	b := createCheckInFixture(t, signer, "sold", now.Add(-time.Hour), now.Add(time.Hour))
	pool := requireIntegrationDB(t)
	snapshot := func(id int64) string {
		t.Helper()
		var row string
		if err := pool.QueryRow(context.Background(), `SELECT row_to_json(t)::text FROM ticketing.tickets t WHERE id=$1`, id).Scan(&row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	beforeA, beforeB := snapshot(a.TicketID), snapshot(b.TicketID)
	for i := 0; i < 2; i++ {
		got, err := svc.VerifyTicketCredential(checkInAuthorizedContext(a), a.Credential, a.EventPublicID)
		if err != nil || got == nil || !got.CanCheckIn {
			t.Fatalf("authorized validation: %v %v", got, err)
		}
	}
	for _, tc := range []struct {
		name              string
		ctx               context.Context
		credential, event string
		want              error
	}{
		{"anonymous", context.Background(), a.Credential, a.EventPublicID, ErrCheckInAuthenticationRequired},
		{"other event staff before signature", checkInAuthorizedContext(b), "bad-signature", a.EventPublicID, ErrCheckInPermissionDenied},
		{"wrong event ticket", checkInAuthorizedContext(a), b.Credential, a.EventPublicID, ErrTicketCheckInWrongEvent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.VerifyTicketCredential(tc.ctx, tc.credential, tc.event)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
	if snapshot(a.TicketID) != beforeA || snapshot(b.TicketID) != beforeB {
		t.Fatal("Validate modified ticket rows")
	}

	// Validation must leave the ticket usable by the existing atomic check-in.
	ctx := checkInAuthorizedContext(a)
	req := &ticketdto.CheckInTicketRequest{Credential: a.Credential, EventID: a.EventPublicID, Method: "qr_code"}
	checked, err := svc.CheckInTicket(ctx, req)
	if err != nil || checked == nil || !checked.Accepted {
		t.Fatalf("check-in after validation: result=%v err=%v", checked, err)
	}
	var actor int64
	var count int
	var state string
	var checkedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT checked_in_by, validation_count, status, checked_in_at FROM ticketing.tickets WHERE id=$1`, a.TicketID).Scan(&actor, &count, &state, &checkedAt); err != nil {
		t.Fatal(err)
	}
	if actor != a.UserID || count != 1 || state != "checked_in" || checkedAt == nil {
		t.Fatalf("unexpected check-in record: actor=%d count=%d status=%s checked_at=%v", actor, count, state, checkedAt)
	}
	afterCheckIn := snapshot(a.TicketID)
	used, err := svc.VerifyTicketCredential(ctx, a.Credential, a.EventPublicID)
	if err != nil || used == nil || !used.Authentic || used.CanCheckIn || used.Result != "ALREADY_CHECKED_IN" {
		t.Fatalf("validate consumed ticket: result=%v err=%v", used, err)
	}
	second, err := svc.CheckInTicket(ctx, req)
	if err != nil || second == nil || second.Accepted || second.Result != "ALREADY_CHECKED_IN" {
		t.Fatalf("second check-in: result=%v err=%v", second, err)
	}
	if snapshot(a.TicketID) != afterCheckIn || snapshot(b.TicketID) != beforeB {
		t.Fatal("validation or repeated check-in changed persisted ticket rows")
	}
}
