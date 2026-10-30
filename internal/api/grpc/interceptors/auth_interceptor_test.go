package interceptors

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	osmicontext "github.com/osmitickets-stack/osmi-server/internal/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestIdentityRequiresVerifiedGateway(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"osmi-gateway"}}
	for _, tc := range []struct {
		name  string
		state *tls.ConnectionState
		allow bool
	}{
		{"no peer", nil, false},
		{"TLS without verified chain", &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}}, false},
		{"verified other service", &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{DNSNames: []string{"worker"}}}}, false},
		{"wildcard", &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{DNSNames: []string{"*"}}}}, false},
		{"verified gateway", &tls.ConnectionState{Version: tls.VersionTLS13, VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.state != nil {
				ctx = peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{State: *tc.state}})
			}
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("user-id", "11111111-1111-4111-8111-111111111111"))
			called := false
			_, err := NewAuthUnaryInterceptor("osmi-gateway")(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, req interface{}) (interface{}, error) {
				called = true
				id, ok := osmicontext.GetUserID(ctx)
				if !ok || id != "11111111-1111-4111-8111-111111111111" {
					t.Fatal("identity lost")
				}
				return nil, nil
			})
			if tc.allow {
				if err != nil || !called {
					t.Fatal(err)
				}
			} else if status.Code(err) != codes.Unauthenticated || called {
				t.Fatalf("untrusted gateway accepted: %v", err)
			}
		})
	}
}
