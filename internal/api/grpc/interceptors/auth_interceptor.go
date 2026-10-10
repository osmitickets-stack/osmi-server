package interceptors

import (
	"context"

	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// AuthUnaryInterceptor propaga al contexto interno el public UUID
// del usuario autenticado por el gateway.
func AuthUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		userIDs := md.Get("user-id")

		if len(userIDs) > 0 && userIDs[0] != "" {
			ctx = osmicontext.WithUserID(
				ctx,
				userIDs[0],
			)
		}
	}

	return handler(ctx, req)
}
