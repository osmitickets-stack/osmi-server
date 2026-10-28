package grpc

import (
	"context"
	"errors"
	"testing"

	pb "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/application/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type validationAuthorizer struct{ err error }

func (a validationAuthorizer) Authorize(context.Context, string) (*services.CheckInPrincipal, error) {
	return nil, a.err
}

func TestValidateCredentialHandlerErrors(t *testing.T) {
	const event = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name string
		req  *pb.ValidateTicketCredentialRequest
		err  error
		want codes.Code
	}{
		{"nil", nil, nil, codes.InvalidArgument},
		{"empty credential", &pb.ValidateTicketCredentialRequest{EventId: event}, nil, codes.InvalidArgument},
		{"missing event", &pb.ValidateTicketCredentialRequest{Credential: "qr"}, nil, codes.InvalidArgument},
		{"invalid event", &pb.ValidateTicketCredentialRequest{Credential: "qr", EventId: "bad"}, nil, codes.InvalidArgument},
		{"anonymous", &pb.ValidateTicketCredentialRequest{Credential: "bad-signature", EventId: event}, services.ErrCheckInAuthenticationRequired, codes.Unauthenticated},
		{"inactive", &pb.ValidateTicketCredentialRequest{Credential: "bad-signature", EventId: event}, services.ErrCheckInUserInactive, codes.Unauthenticated},
		{"forbidden", &pb.ValidateTicketCredentialRequest{Credential: "bad-signature", EventId: event}, services.ErrCheckInPermissionDenied, codes.PermissionDenied},
		{"internal", &pb.ValidateTicketCredentialRequest{Credential: "bad-signature", EventId: event}, errors.New("private database details"), codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := services.NewTicketService(nil, nil, nil, nil, nil, nil)
			svc.SetCheckInAuthorization(validationAuthorizer{err: tc.err})
			got, err := NewTicketHandler(svc).ValidateTicketCredential(context.Background(), tc.req)
			if got != nil || status.Code(err) != tc.want {
				t.Fatalf("got=%v err=%v want=%v", got, err, tc.want)
			}
			if tc.name == "internal" && status.Convert(err).Message() != "failed to validate ticket credential" {
				t.Fatal("internal details leaked")
			}
		})
	}
}
