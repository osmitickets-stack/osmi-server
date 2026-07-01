package grpc

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
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
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", s.config.GRPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC port: %w", err)
	}

	s.grpcServer = grpc.NewServer()

	// Registrar servicios
	pb.RegisterOsmiServiceServer(s.grpcServer, s.grpcHandler)

	// Para desarrollo/testing
	reflection.Register(s.grpcServer)

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
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Crear mux para gRPC-Gateway
	mux := runtime.NewServeMux(
		runtime.WithErrorHandler(s.customErrorHandler),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
	)

	// Registrar handlers HTTP
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	err := pb.RegisterOsmiServiceHandlerFromEndpoint(
		ctx,
		mux,
		fmt.Sprintf(":%s", s.config.GRPCPort),
		opts,
	)
	if err != nil {
		return fmt.Errorf("failed to register gateway: %w", err)
	}

	// Configurar router HTTP con middleware
	router := s.setupRouter(mux)

	s.httpServer = &http.Server{
		Addr:    ":8080", // cámbialo cuando agregues HTTPPort al Config
		Handler: router,
	}

	s.logger.Info("🌐 HTTP Gateway starting",
		zap.String("address", s.httpServer.Addr),
	)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Fatal("HTTP gateway failed", zap.Error(err))
		}
	}()

	return nil
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
