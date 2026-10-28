package services

import (
	"context"
	"errors"
	"testing"
	"time"
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
}
