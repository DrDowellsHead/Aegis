package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc/credentials"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// ServerCredentials performs proactive leaf validity and server-purpose
// checks before accepting any inbound TLS connections.
func ServerCredentials(certPath, keyPath string) (credentials.TransportCredentials, error) {
	return ServerCredentialsForHost(certPath, keyPath, "", "")
}

// ServerCredentialsForHost additionally checks the expected SAN and optional
// issuer chain. The recipient must still independently verify that SAN
// against its actual connection target.
func ServerCredentialsForHost(certPath, keyPath, rootsPath, hostname string) (credentials.TransportCredentials, error) {
	if _, err := identity.ValidateServerCertificate(certPath, keyPath, rootsPath, hostname, 0); err != nil {
		return nil, fmt.Errorf("invalid server TLS certificate: %w", err)
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

// ClientCredentials accepts one or more explicit, valid CA certificates to
// support two-phase CA rollover without InsecureSkipVerify.
func ClientCredentials(caPath string) (credentials.TransportCredentials, error) {
	roots, _, err := identity.LoadTrustBundle(caPath)
	if err != nil {
		return nil, fmt.Errorf("load TLS trust: %w", err)
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
