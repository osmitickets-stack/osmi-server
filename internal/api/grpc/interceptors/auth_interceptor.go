package interceptors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// UnaryServerInterceptor extrae user-id de la metadata gRPC y la pone en el contexto
func AuthUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		userIDs := md.Get("user-id")
		if len(userIDs) > 0 {
			ctx = context.WithValue(ctx, "user_id", userIDs[0])
		}
	}

	return handler(ctx, req)
}
