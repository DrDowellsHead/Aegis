package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/DrDowellsHead/Aegis/internal/identity"
	"path/filepath"
	"testing"
)

func TestTLSCommandCheckAndCABundle(t *testing.T) {
	d := t.TempDir()
	oldCA, oldCert, oldKey := filepath.Join(d, "old-ca.pem"), filepath.Join(d, "old-leaf.pem"), filepath.Join(d, "old-key.pem")
	newCA, newCert, newKey := filepath.Join(d, "new-ca.pem"), filepath.Join(d, "new-leaf.pem"), filepath.Join(d, "new-key.pem")
	if err := identity.GenerateDevelopmentTLS("server.example.test", oldCA, oldCert, oldKey); err != nil {
		t.Fatal(err)
	}
	if err := identity.GenerateDevelopmentTLS("server.example.test", newCA, newCert, newKey); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	check := []string{"tls", "check", "--tls-cert", oldCert, "--tls-key", oldKey, "--ca-cert", oldCA, "--host", "server.example.test", "--warn-before", "0s"}
	if code := Run(context.Background(), check, &out, &errOut, "test"); code != 0 {
		t.Fatalf("TLS check %d: %s", code, errOut.String())
	}
	var report identity.ServerCertificateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.ChainVerified {
		t.Fatalf("bad report %+v %v", report, err)
	}
	out.Reset()
	errOut.Reset()
	check[9] = "other.example.test"
	if code := Run(context.Background(), check, &out, &errOut, "test"); code != 1 {
		t.Fatalf("bad SAN code %d", code)
	}
	out.Reset()
	errOut.Reset()
	bundle := filepath.Join(d, "bundle.pem")
	if code := Run(context.Background(), []string{"tls", "bundle", "--current-ca", oldCA, "--next-ca", newCA, "--output", bundle}, &out, &errOut, "test"); code != 0 {
		t.Fatalf("bundle %d: %s", code, errOut.String())
	}
	if _, err := identity.ValidateServerCertificate(newCert, newKey, bundle, "server.example.test", 0); err != nil {
		t.Fatal(err)
	}
}
func TestTLSCommandRejectsIncompleteOptions(t *testing.T) {
	for _, args := range [][]string{{"tls"}, {"tls", "unknown"}, {"tls", "check"}, {"tls", "bundle"}, {"tls", "check", "--warn-before", "-1s"}, {"tls", "bundle", "extra"}} {
		var out, errOut bytes.Buffer
		if code := Run(context.Background(), args, &out, &errOut, "test"); code != 2 {
			t.Fatalf("%v code=%d %s", args, code, errOut.String())
		}
	}
}
