package grpc

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	pb "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/config"

	appgrpc "github.com/osmitickets-stack/osmi-server/internal/application/handlers/grpc"
)

type Server struct {
	config      *config.Config
	logger      *zap.Logger
	grpcServer  *grpc.Server
	httpServer  *http.Server
	grpcHandler *appgrpc.Handler
}

func NewServer(
	cfg *config.Config,
	logger *zap.Logger,
	grpcHandler *appgrpc.Handler,
) *Server {
	return &Server{
		config:      cfg,
		logger:      logger,
		grpcHandler: grpcHandler,
	}
}

func (s *Server) StartGRPC() error {
	secureServer, err := NewSecureServer(s.config)
	if err != nil {
		return fmt.Errorf("gRPC security configuration: %w", err)
	}
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", s.config.GRPCPort))
	if err != nil {
		secureServer.Stop()
		return fmt.Errorf("failed to listen on gRPC port: %w", err)
	}

	s.grpcServer = secureServer

	// Registrar servicios
	pb.RegisterOsmiServiceServer(s.grpcServer, s.grpcHandler)

	s.logger.Info("🚀 gRPC server starting",
		zap.String("address", lis.Addr().String()),
		zap.String("port", s.config.GRPCPort),
	)

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Fatal("gRPC server failed", zap.Error(err))
		}
	}()

	return nil
}

func (s *Server) StartHTTPGateway() error {
	return fmt.Errorf("embedded HTTP gateway is disabled; use osmi-gateway with mutual TLS")
}

func (s *Server) setupRouter(gwMux *runtime.ServeMux) http.Handler {
	// Este método será implementado en el router HTTP
	// Por ahora, retornamos el mux directamente
	return gwMux
}

func (s *Server) customErrorHandler(
	ctx context.Context,
	mux *runtime.ServeMux,
	marshaler runtime.Marshaler,
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	st, _ := status.FromError(err)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(runtime.HTTPStatusFromCode(st.Code()))

	_ = marshaler.NewEncoder(w).Encode(map[string]any{
		"error": st.Message(),
		"code":  st.Code().String(),
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("HTTP shutdown error: %w", err))
		}
	}

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}

	return nil
}
