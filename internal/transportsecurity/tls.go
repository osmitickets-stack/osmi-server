// Package transportsecurity authenticates the Gateway before trusting its metadata.
package transportsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
)

type Config struct {
	CAFile         string
	CertFile       string
	KeyFile        string
	GatewayDNSName string
}

// VerifiedGateway requires a verified chain and an exact DNS SAN, never a wildcard or CommonName.
func VerifiedGateway(state tls.ConnectionState, gatewayDNSName string) bool {
	if state.Version < tls.VersionTLS13 || len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 || gatewayDNSName == "" {
		return false
	}
	for _, name := range state.PeerCertificates[0].DNSNames {
		if strings.EqualFold(name, gatewayDNSName) {
			return true
		}
	}
	return false
}

func LoadServerCredentials(cfg Config) (credentials.TransportCredentials, error) {
	if strings.TrimSpace(cfg.CAFile) == "" || strings.TrimSpace(cfg.CertFile) == "" || strings.TrimSpace(cfg.KeyFile) == "" || strings.TrimSpace(cfg.GatewayDNSName) == "" || strings.ContainsAny(cfg.GatewayDNSName, "* /\\\t\r\n") {
		return nil, errors.New("gRPC requires CA, server certificate, private key and exact gateway DNS identity")
	}
	certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load gRPC server certificate: %w", err)
	}
	pem, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("load gRPC client CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("gRPC client CA contains no certificates")
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			if !VerifiedGateway(state, cfg.GatewayDNSName) {
				return errors.New("untrusted gateway service identity")
			}
			return nil
		},
	}), nil
}
