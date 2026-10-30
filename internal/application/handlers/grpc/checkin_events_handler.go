package grpc

import (
	"context"
	"errors"
	pb "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/application/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

func (h *Handler) ListCheckInEvents(ctx context.Context, req *pb.ListCheckInEventsRequest) (*pb.ListCheckInEventsResponse, error) {
	return h.ticketHandler.ListCheckInEvents(ctx, req)
}

func (h *TicketHandler) ListCheckInEvents(ctx context.Context, req *pb.ListCheckInEventsRequest) (*pb.ListCheckInEventsResponse, error) {
	page, err := h.ticketService.ListCheckInEvents(ctx, req.GetCursor())
	if err != nil {
		switch {
		case errors.Is(err, services.ErrCheckInAuthenticationRequired), errors.Is(err, services.ErrCheckInUserInactive):
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		case errors.Is(err, services.ErrCheckInCursorInvalid):
			return nil, status.Error(codes.InvalidArgument, "invalid cursor")
		default:
			return nil, status.Error(codes.Internal, "could not load check-in events")
		}
	}
	result := &pb.ListCheckInEventsResponse{NextCursor: page.NextCursor, Events: make([]*pb.CheckInEvent, 0, len(page.Events))}
	for _, event := range page.Events {
		result.Events = append(result.Events, &pb.CheckInEvent{PublicId: event.PublicID, Name: event.Name, StartsAt: event.StartsAt.Format(time.RFC3339)})
	}
	return result, nil
}
