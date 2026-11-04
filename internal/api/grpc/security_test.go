package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/osmitickets-stack/osmi-protobuf/gen/pb"
	"github.com/osmitickets-stack/osmi-server/internal/config"
	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"github.com/osmitickets-stack/osmi-server/internal/testutil/grpcpki"
	"github.com/osmitickets-stack/osmi-server/internal/transportsecurity"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

type identityProbe struct {
	pb.UnimplementedOsmiServiceServer
	calls atomic.Int32
}

func (p *identityProbe) HealthCheck(ctx context.Context, _ *pb.Empty) (*pb.HealthResponse, error) {
	p.calls.Add(1)
	id, _ := osmicontext.GetUserID(ctx)
	return &pb.HealthResponse{Status: id}, nil
}

func TestGatewayMTLSBoundary(t *testing.T) {
	ca := grpcpki.New(t)
	serverID := ca.Issue(t, "osmi-server", x509.ExtKeyUsageServerAuth, false)
	gatewayID := ca.Issue(t, "osmi-gateway", x509.ExtKeyUsageClientAuth, false)
	cfg := &config.Config{GRPCTLS: transportsecurity.Config{CAFile: ca.File, CertFile: serverID.CertFile, KeyFile: serverID.KeyFile, GatewayDNSName: "osmi-gateway"}, GRPCReflection: true}
	server, err := NewSecureServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	probe := &identityProbe{}
	pb.RegisterOsmiServiceServer(server, probe)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(lis)
	t.Cleanup(server.Stop)
	roots := x509.NewCertPool()
	data, err := os.ReadFile(ca.File)
	if err != nil {
		t.Fatal(err)
	}
	roots.AppendCertsFromPEM(data)
	foreign := grpcpki.New(t)
	for _, tc := range []struct {
		name       string
		identity   *grpcpki.Identity
		serverName string
		plain      bool
		tls12      bool
		deny       bool
	}{
		{name: "gateway", identity: &gatewayID},
		{name: "no client certificate", deny: true},
		{name: "other service signed by same CA", identity: identityPointer(ca.Issue(t, "osmi-worker", x509.ExtKeyUsageClientAuth, false)), deny: true},
		{name: "foreign CA", identity: identityPointer(foreign.Issue(t, "osmi-gateway", x509.ExtKeyUsageClientAuth, false)), deny: true},
		{name: "expired certificate", identity: identityPointer(ca.Issue(t, "osmi-gateway", x509.ExtKeyUsageClientAuth, true)), deny: true},
		{name: "wrong certificate purpose", identity: identityPointer(ca.Issue(t, "osmi-gateway", x509.ExtKeyUsageServerAuth, false)), deny: true},
		{name: "wrong server hostname", identity: &gatewayID, serverName: "attacker", deny: true},
		{name: "plaintext direct access", plain: true, deny: true},
		{name: "TLS12 rejected", identity: &gatewayID, tls12: true, deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var creds credentials.TransportCredentials
			if tc.plain {
				creds = insecure.NewCredentials()
			} else {
				conf := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "osmi-server"}
				if tc.serverName != "" {
					conf.ServerName = tc.serverName
				}
				if tc.tls12 {
					conf.MinVersion = tls.VersionTLS12
					conf.MaxVersion = tls.VersionTLS12
				}
				if tc.identity != nil {
					cert, err := tls.LoadX509KeyPair(tc.identity.CertFile, tc.identity.KeyFile)
					if err != nil {
						t.Fatal(err)
					}
					conf.Certificates = []tls.Certificate{cert}
				}
				creds = credentials.NewTLS(conf)
			}
			conn, err := grpcgo.NewClient(lis.Addr().String(), grpcgo.WithTransportCredentials(creds))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("user-id", "11111111-1111-4111-8111-111111111111"))
			before := probe.calls.Load()
			result, err := pb.NewOsmiServiceClient(conn).HealthCheck(ctx, &pb.Empty{})
			if tc.deny {
				if err == nil || probe.calls.Load() != before {
					t.Fatalf("untrusted transport reached handler: %v %v", result, err)
				}
				return
			}
			if err != nil || result.Status != "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("trusted identity lost: %v %v", result, err)
			}
			for _, ids := range [][]string{{""}, {"not-a-uuid"}, {"00000000-0000-0000-0000-000000000000"}, {"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}} {
				badCtx := metadata.NewOutgoingContext(ctx, metadata.MD{"user-id": ids})
				before := probe.calls.Load()
				_, err := pb.NewOsmiServiceClient(conn).HealthCheck(badCtx, &pb.Empty{})
				if status.Code(err) != codes.Unauthenticated || probe.calls.Load() != before {
					t.Fatalf("invalid identity accepted: %v %v", ids, err)
				}
			}
			anonymous := metadata.NewOutgoingContext(ctx, metadata.MD{})
			result, err = pb.NewOsmiServiceClient(conn).HealthCheck(anonymous, &pb.Empty{})
			if err != nil || result.Status != "" {
				t.Fatalf("anonymous public RPC: %v %v", result, err)
			}
			stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(anonymous)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.CloseSend()
			if err := stream.Send(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: ""}}); err != nil {
				t.Fatal(err)
			}
			if _, err = stream.Recv(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func identityPointer(id grpcpki.Identity) *grpcpki.Identity { return &id }

func TestSecureServerRequiresConfiguration(t *testing.T) {
	if _, err := NewSecureServer(&config.Config{}); err == nil {
		t.Fatal("missing TLS configuration accepted")
	}
	s := NewServer(&config.Config{}, nil, nil)
	if err := s.StartHTTPGateway(); err == nil {
		t.Fatal("embedded HTTP bypass accepted")
	}
}
