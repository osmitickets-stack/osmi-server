package grpc

import (
	"context"

	osmi "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/application/services"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type WebhookHandler struct {
	osmi.UnimplementedOsmiServiceServer
	paymentService *services.PaymentService
}

func NewWebhookHandler(paymentService *services.PaymentService) *WebhookHandler {
	return &WebhookHandler{
		paymentService: paymentService,
	}
}

func (h *WebhookHandler) HandleWebhook(ctx context.Context, req *osmi.WebhookRequest) (*osmi.Empty, error) {
	if req.Payload == "" {
		return nil, status.Error(codes.InvalidArgument, "payload is required")
	}
	if req.SignatureHeader == "" {
		return nil, status.Error(codes.InvalidArgument, "signature_header is required")
	}

	err := h.paymentService.HandleWebhook(ctx, []byte(req.Payload), req.SignatureHeader)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &osmi.Empty{}, nil
}
