package services

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListCheckInEventsIntegration(t *testing.T) {
	service, signer := newCheckInTestService(t) // validates isolated database marker before writes
	now := time.Now()
	a := createCheckInFixture(t, signer, "sold", now.Add(-time.Hour), now.Add(time.Hour))
	b := createCheckInFixture(t, signer, "sold", now.Add(-time.Hour), now.Add(time.Hour))
	ctx := checkInAuthorizedContext(a)
	page, err := service.ListCheckInEvents(ctx, "")
	if err != nil || page == nil || len(page.Events) != 1 || page.Events[0].PublicID != a.EventPublicID {
		t.Fatalf("staff must see only assigned event: %v %v", page, err)
	}
	denied, err := service.ListCheckInEvents(context.Background(), "")
	if denied != nil || !errors.Is(err, ErrCheckInAuthenticationRequired) {
		t.Fatalf("guest: %v %v", denied, err)
	}
	pool := requireIntegrationDB(t)
	if _, err := pool.Exec(ctx, `UPDATE ticketing.event_staff SET is_active=false WHERE event_id=$1 AND user_id=$2`, a.EventID, a.UserID); err != nil {
		t.Fatal(err)
	}
	page, err = service.ListCheckInEvents(ctx, "")
	if err != nil || page == nil || len(page.Events) != 0 {
		t.Fatalf("revoked staff must see no events: %v %v", page, err)
	}
	assertCheckInTicketUnchanged(t, a.TicketID)
	assertCheckInTicketUnchanged(t, b.TicketID)
}
