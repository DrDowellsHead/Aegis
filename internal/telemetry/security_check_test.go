package telemetry

import (
	"github.com/DrDowellsHead/Aegis/internal/identity"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryTLSCertificatePreflightAndExplicitRoots(t *testing.T) {
	d := t.TempDir()
	ca, cert, key := filepath.Join(d, "ca.pem"), filepath.Join(d, "server.pem"), filepath.Join(d, "server.key")
	if err := identity.GenerateDevelopmentTLS("example.test", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCredentials(cert, key); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCredentialsForHost(cert, key, ca, "example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCredentialsForHost(cert, key, ca, "other.test"); err == nil {
		t.Fatal("wrong expected hostname accepted")
	}
	if _, err := ClientCredentials(ca); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientCredentials(cert); err == nil {
		t.Fatal("leaf as CA accepted")
	}
	bad := filepath.Join(d, "bad.key")
	if err := os.WriteFile(bad, []byte("bad key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerCredentials(cert, bad); err == nil {
		t.Fatal("invalid key accepted")
	}
}
