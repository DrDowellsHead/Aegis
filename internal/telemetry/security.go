package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// ServerCredentials requires a certificate chain and the matching private key.
func ServerCredentials(certPath, keyPath string) (credentials.TransportCredentials, error) {
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("TLS certificate and key are required")
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pair},
		NextProtos:   []string{"h2"},
	}), nil
}

// ClientCredentials trusts only the explicitly supplied CA; it never skips
// chain, expiry or DNS/IP subject alternative name verification.
func ClientCredentials(caPath string) (credentials.TransportCredentials, error) {
	if caPath == "" {
		return nil, fmt.Errorf("CA certificate path is required")
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA certificate contains no usable PEM certificates")
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		NextProtos: []string{"h2"},
	}), nil
}

// bearer is sent through TLS-protected gRPC metadata, not the protobuf payload.
// grpc refuses to send it on insecure transport.
type bearer struct{ token string }

func (b bearer) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}
func (b bearer) RequireTransportSecurity() bool { return true }
