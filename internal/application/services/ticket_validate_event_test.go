package services

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/osmitickets-stack/osmi-server/internal/domain/entities"
	"github.com/osmitickets-stack/osmi-server/internal/domain/repository"
	"github.com/osmitickets-stack/osmi-server/internal/shared/security"
)

// Any call other than GetByPublicID (including a write or transaction) panics.
type validationTicketRepo struct {
	repository.TicketRepository
	ticket *entities.Ticket
	err    error
	reads  int
}

func (r *validationTicketRepo) GetByPublicID(_ context.Context, id string) (*entities.Ticket, error) {
	r.reads++
	return r.ticket, r.err
}

func TestValidateEventAuthorizationBeforeSignature(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*authTestFixture)
		want      error
	}{
		{"guest", func(f *authTestFixture) { f.ctx = context.Background() }, ErrCheckInAuthenticationRequired},
		{"inactive", func(f *authTestFixture) { f.user.IsActive = false }, ErrCheckInUserInactive},
		{"customer", func(f *authTestFixture) {}, ErrCheckInPermissionDenied},
		{"other event staff", func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.EventID = 999 }, ErrCheckInPermissionDenied},
		{"revoked staff", func(f *authTestFixture) { f.assignStaff("checkin_staff"); f.staff.IsActive = false }, ErrCheckInPermissionDenied},
		{"owner", func(f *authTestFixture) { f.organizer.UserID = authInt64Ptr(42) }, ErrTicketCredentialInvalid},
		{"superuser", func(f *authTestFixture) { f.user.IsSuperuser = true }, ErrTicketCredentialInvalid},
		{"staff", func(f *authTestFixture) { f.assignStaff("checkin_staff") }, ErrTicketCredentialInvalid},
		{"manager", func(f *authTestFixture) { f.assignStaff("event_manager") }, ErrTicketCredentialInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthTestFixture()
			tc.configure(f)
			signer, err := security.NewTicketCredentialService("k1", []byte("0123456789abcdef0123456789abcdef"))
			if err != nil {
				t.Fatal(err)
			}
			svc := NewTicketService(nil, nil, nil, nil, nil, signer)
			svc.SetCheckInAuthorization(f.service())
			got, err := svc.VerifyTicketCredential(f.ctx, "invalid-signature", authTestEventPublicID)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("result=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestValidateEventReadOnly(t *testing.T) {
	signer, err := security.NewTicketCredentialService("k1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	const ticketID = "33333333-3333-4333-8333-333333333333"
	credential, err := signer.Sign(ticketID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"sold", "checked_in", "reserved", "refunded", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			ticket := &entities.Ticket{PublicID: ticketID, EventID: 88, Status: state}
			before := *ticket
			repo := &validationTicketRepo{ticket: ticket}
			events := authEventRepoStub{find: func(_ context.Context, id string) (*entities.Event, error) {
				if id != authTestEventPublicID {
					t.Fatal(id)
				}
				return &entities.Event{ID: 88}, nil
			}}
			svc := NewTicketService(repo, nil, events, nil, nil, signer)
			svc.SetCheckInAuthorization(&boundaryAuthorizerStub{principal: &CheckInPrincipal{UserID: 42, UserPublicID: authTestUserPublicID, AccessRole: "checkin_staff"}})
			for i := 0; i < 2; i++ {
				got, err := svc.VerifyTicketCredential(context.Background(), credential, authTestEventPublicID)
				if err != nil || got == nil {
					t.Fatalf("%v %v", got, err)
				}
				if got.CanCheckIn != (state == "sold") {
					t.Fatalf("unexpected eligibility: %+v", got)
				}
			}
			if !reflect.DeepEqual(before, *ticket) || repo.reads != 2 {
				t.Fatal("validation changed ticket or unexpected reads")
			}
			ticket.EventID = 99
			before = *ticket
			got, err := svc.VerifyTicketCredential(context.Background(), credential, authTestEventPublicID)
			if got != nil || !errors.Is(err, ErrTicketCheckInWrongEvent) {
				t.Fatalf("wrong event: %v %v", got, err)
			}
			if !reflect.DeepEqual(before, *ticket) {
				t.Fatal("rejection changed ticket")
			}
			repo.ticket = nil
			repo.err = repository.ErrTicketNotFound
			_, err = svc.VerifyTicketCredential(context.Background(), credential, authTestEventPublicID)
			if !errors.Is(err, ErrTicketCredentialTicketNotFound) {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateEventInputAndInvalidPrincipal(t *testing.T) {
	for _, event := range []string{"", "bad-uuid"} {
		svc := NewTicketService(nil, nil, nil, nil, nil, nil)
		_, err := svc.ValidateTicketCredential(context.Background(), "qr", event)
		want := ErrTicketCheckInEventInvalid
		if event == "" {
			want = ErrTicketCheckInEventRequired
		}
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
	for _, p := range []*CheckInPrincipal{nil, {}, {UserID: -1, UserPublicID: "u", AccessRole: "staff"}, {UserID: 1, AccessRole: "staff"}, {UserID: 1, UserPublicID: "u"}} {
		svc := NewTicketService(nil, nil, nil, nil, nil, nil)
		svc.SetCheckInAuthorization(&boundaryAuthorizerStub{principal: p})
		got, err := svc.ValidateTicketCredential(context.Background(), "qr", authTestEventPublicID)
		if got != nil || !errors.Is(err, ErrCheckInPrincipalInvalid) {
			t.Fatalf("%v %v", got, err)
		}
	}
}
