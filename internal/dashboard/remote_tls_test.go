package dashboard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

func TestRemoteDashboardRealTLSConnection(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	cert := filepath.Join(dir, "server.pem")
	key := filepath.Join(dir, "server.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	if err := CheckServerCertificate(cert, key, "https://127.0.0.1:8443"); err != nil {
		t.Fatal(err)
	}
	if err := CheckServerCertificate(cert, key, "https://different.example"); err == nil {
		t.Fatal("TLS certificate hostname mismatch was accepted")
	}

	users := filepath.Join(dir, "users.json")
	if err := SaveAccount(users, "operator", RoleAdmin, "example-strong-operator-password", false, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Aegis</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://" + listener.Addr().String()
	handler, err := NewRemote(&stub{}, users, origin, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeRemote(ctx, listener, handler, slog.New(slog.NewTextHandler(io.Discard, nil)), cert, key)
	}()

	caPEM, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("invalid generated CA")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(origin + "/api/web/session")
	if err != nil {
		t.Fatalf("TLS session endpoint unavailable: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"mode":"remote"`) {
		t.Fatalf("unexpected HTTPS response %d: %s", response.StatusCode, body)
	}
	if response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		t.Fatal("dashboard did not negotiate TLS 1.3")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TLS dashboard shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TLS dashboard failed to shut down")
	}
}
