package grpc

import (
	"time"

	"github.com/osmitickets-stack/osmi-server/internal/api/grpc/interceptors"
	"github.com/osmitickets-stack/osmi-server/internal/config"
	"github.com/osmitickets-stack/osmi-server/internal/transportsecurity"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

// NewSecureServer is shared by both entry points. There is no plaintext mode.
func NewSecureServer(cfg *config.Config) (*grpcgo.Server, error) {
	creds, err := transportsecurity.LoadServerCredentials(cfg.GRPCTLS)
	if err != nil {
		return nil, err
	}
	server := grpcgo.NewServer(
		grpcgo.Creds(creds),
		grpcgo.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 30 * time.Second, PermitWithoutStream: true}),
		grpcgo.KeepaliveParams(keepalive.ServerParameters{MaxConnectionAge: 15 * time.Minute, MaxConnectionAgeGrace: time.Minute, Time: 2 * time.Minute, Timeout: 20 * time.Second}),
		grpcgo.UnaryInterceptor(interceptors.NewAuthUnaryInterceptor(cfg.GRPCTLS.GatewayDNSName)),
		grpcgo.StreamInterceptor(interceptors.NewAuthStreamInterceptor(cfg.GRPCTLS.GatewayDNSName)),
		grpcgo.MaxRecvMsgSize(10*1024*1024),
		grpcgo.MaxSendMsgSize(10*1024*1024),
	)
	if cfg.GRPCReflection {
		reflection.Register(server)
	}
	return server, nil
}
