package grpc

import (
	"context"
	"testing"

	osmi "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListTicketsRejectsMissingTrustedIdentity(t *testing.T) {
	handler := NewTicketHandler(nil)
	_, err := handler.ListTickets(context.Background(), &osmi.ListTicketsRequest{CustomerId: "1"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("list tickets without trusted identity: %v", err)
	}
}
