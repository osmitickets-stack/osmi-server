package services

import (
	"context"
	"errors"
	"testing"

	ticketdto "github.com/osmitickets-stack/osmi-server/internal/api/dto/ticket"
)

type boundaryAuthorizerStub struct {
	principal *CheckInPrincipal
	err       error
	called    int
}

func (a *boundaryAuthorizerStub) Authorize(_ context.Context, _ string) (*CheckInPrincipal, error) {
	a.called++
	return a.principal, a.err
}

func TestCheckInAuthorizationHappensBeforeQRVerification(t *testing.T) {
	stub := &boundaryAuthorizerStub{err: ErrCheckInPermissionDenied}
	service := NewTicketService(nil, nil, nil, nil, nil, nil)
	service.SetCheckInAuthorization(stub)

	got, err := service.CheckInTicket(context.Background(), &ticketdto.CheckInTicketRequest{
		Credential: "not-a-valid-credential",
		EventID:    "22222222-2222-4222-8222-222222222222",
	})
	if !errors.Is(err, ErrCheckInPermissionDenied) {
		t.Fatalf("unauthorized caller should be denied before QR verification: %v", err)
	}
	if got != nil {
		t.Fatalf("unauthorized caller received result: %+v", got)
	}
	if stub.called != 1 {
		t.Fatalf("expected one authorization check; got %d", stub.called)
	}
}

func TestCheckInRejectsInvalidAuthorizationPrincipals(t *testing.T) {
	cases := []struct {
		name      string
		principal *CheckInPrincipal
	}{
		{
			name: "nil principal",
		},
		{
			name: "zero user ID",
			principal: &CheckInPrincipal{
				UserID:       0,
				UserPublicID: "user-public-id",
				AccessRole:   "checkin_staff",
			},
		},
		{
			name: "negative user ID",
			principal: &CheckInPrincipal{
				UserID:       -1,
				UserPublicID: "user-public-id",
				AccessRole:   "checkin_staff",
			},
		},
		{
			name: "empty user public ID",
			principal: &CheckInPrincipal{
				UserID:     42,
				AccessRole: "checkin_staff",
			},
		},
		{
			name: "empty access role",
			principal: &CheckInPrincipal{
				UserID:       42,
				UserPublicID: "user-public-id",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &boundaryAuthorizerStub{
				principal: tc.principal,
			}

			service := NewTicketService(
				nil, nil, nil, nil, nil, nil,
			)
			service.SetCheckInAuthorization(stub)

			got, err := service.CheckInTicket(
				context.Background(),
				&ticketdto.CheckInTicketRequest{
					Credential: "not-a-valid-credential",
					EventID:    "22222222-2222-4222-8222-222222222222",
				},
			)

			if !errors.Is(err, ErrCheckInPrincipalInvalid) {
				t.Fatalf(
					"expected ErrCheckInPrincipalInvalid, got %v",
					err,
				)
			}

			if got != nil {
				t.Fatalf(
					"invalid principal returned result: %+v",
					got,
				)
			}

			if stub.called != 1 {
				t.Fatalf(
					"expected one authorization call, got %d",
					stub.called,
				)
			}
		})
	}
}
