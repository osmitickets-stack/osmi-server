package interceptors

import (
	"context"

	"github.com/google/uuid"
	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/transportsecurity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func trustedContext(ctx context.Context, gatewayDNSName string) (context.Context, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authenticated gateway connection required")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || !transportsecurity.VerifiedGateway(info.State, gatewayDNSName) {
		return nil, status.Error(codes.Unauthenticated, "authenticated gateway connection required")
	}
	md, _ := metadata.FromIncomingContext(ctx)
	ids := md.Get("user-id")
	ctx = osmicontext.WithUserID(ctx, "")
	if len(ids) == 0 {
		return ctx, nil
	}
	if len(ids) != 1 {
		return nil, status.Error(codes.Unauthenticated, "invalid forwarded user identity")
	}
	id, err := uuid.Parse(ids[0])
	if err != nil || id == uuid.Nil || id.String() != ids[0] {
		return nil, status.Error(codes.Unauthenticated, "invalid forwarded user identity")
	}
	return osmicontext.WithUserID(ctx, ids[0]), nil
}
func NewAuthUnaryInterceptor(gatewayDNSName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		trusted, err := trustedContext(ctx, gatewayDNSName)
		if err != nil {
			return nil, err
		}
		return handler(trusted, req)
	}
}

type authenticatedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedStream) Context() context.Context { return s.ctx }
func NewAuthStreamInterceptor(gatewayDNSName string) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := trustedContext(stream.Context(), gatewayDNSName)
		if err != nil {
			return err
		}
		return handler(srv, &authenticatedStream{ServerStream: stream, ctx: ctx})
	}
}
