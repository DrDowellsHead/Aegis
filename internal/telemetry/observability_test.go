package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/observability"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type monitoredStore struct {
	reject atomic.Bool
}

func (s *monitoredStore) Save(context.Context, string, string, *pb.Snapshot) (bool, error) {
	if s.reject.Load() {
		return false, errors.New("simulated sql commit failure")
	}
	return false, nil
}

func (s *monitoredStore) RecordHeartbeat(context.Context, string) error { return nil }

func TestObservedReceiverCountsGRPCSQLAndFailures(t *testing.T) {
	dir := t.TempDir()
	ca, cert, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "server.pem"), filepath.Join(dir, "server.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	registryPath, credentialsPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "credentials.json")
	if _, err := identity.Provision(registryPath, credentialsPath); err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := identity.LoadCredentials(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	tlsCreds, err := ServerCredentials(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	observer := observability.New()
	store := &monitoredStore{}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- ServeReceiverObserved(ctx, l, slog.New(slog.NewTextHandler(io.Discard, nil)),
			tlsCreds, registry, store, DefaultReceiverLimits(), observer)
	}()
	client, err := NewClient(l.Addr().String(), creds, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send(ctx, "0123456789abcdef0123456789abcdef", sample()); err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	store.reject.Store(true)
	if err := client.Send(ctx, "1123456789abcdef0123456789abcdef", sample()); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected SQL failure mapped to Unavailable, got %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("receiver did not shut down")
	}
	var text strings.Builder
	observer.WritePrometheus(&text, nil)
	for _, expected := range []string{
		"aegis_grpc_requests_total{method=\"send\",code=\"OK\"} 1",
		"aegis_grpc_requests_total{method=\"send\",code=\"Unavailable\"} 1",
		"aegis_grpc_requests_total{method=\"heartbeat\",code=\"OK\"} 1",
		"aegis_db_operations_total{operation=\"save\",result=\"success\"} 1",
		"aegis_db_operations_total{operation=\"save\",result=\"error\"} 1",
		"aegis_db_operations_total{operation=\"heartbeat\",result=\"success\"} 1",
		"aegis_grpc_inflight_requests 0",
	} {
		if !strings.Contains(text.String(), expected) {
			t.Fatalf("missing metric %s:\n%s", expected, text.String())
		}
	}
	if strings.Contains(text.String(), creds.AgentID) || strings.Contains(text.String(), creds.Token) {
		t.Fatal("metrics leaked agent identity or credentials")
	}
}
