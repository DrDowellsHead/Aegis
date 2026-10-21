package telemetry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/storage"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type chaosStore struct {
	db         *storage.Postgres
	outage     atomic.Bool
	dropOneACK atomic.Bool
}

func (s *chaosStore) Save(ctx context.Context, agentID, id string, snapshot *pb.Snapshot) (bool, error) {
	if s.outage.Load() {
		return false, errors.New("injected PostgreSQL write outage")
	}
	already, err := s.db.Save(ctx, agentID, id, snapshot)
	if err != nil {
		return false, err
	}
	if s.dropOneACK.CompareAndSwap(true, false) {
		return false, errors.New("injected lost reply after real PostgreSQL commit")
	}
	return already, nil
}

func (s *chaosStore) RecordHeartbeat(ctx context.Context, id string) error {
	if s.outage.Load() {
		return errors.New("injected PostgreSQL heartbeat outage")
	}
	return s.db.RecordHeartbeat(ctx, id)
}

// A real PostgreSQL 17 + TLS/gRPC scenario. The fault wrapper intentionally
// breaks one reply AFTER committing and one request BEFORE committing.
// It never stops the shared CI PostgreSQL service used by other tests.
func TestChaosPostgresOutageLostACKAndReceiverRestart(t *testing.T) {
	dsn := os.Getenv("AEGIS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AEGIS_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	db, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	cert := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	registryFile := filepath.Join(dir, "agents.json")
	credentialsFile := filepath.Join(dir, "client.json")
	if _, err := identity.Provision(registryFile, credentialsFile); err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := identity.LoadCredentials(credentialsFile)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := ServerCredentials(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	store := &chaosStore{db: db}
	start := func() (string, context.CancelFunc, <-chan error) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		serverCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- ServeReceiverWithLimits(serverCtx, listener,
				slog.New(slog.NewTextHandler(io.Discard, nil)), transport, registry, store,
				ReceiverLimits{Measurements: 2, PerAgent: 1, Heartbeats: 1})
		}()
		return listener.Addr().String(), stop, done
	}
	address, stop, stopped := start()
	defer stop()

	client, err := NewClient(address, credentials, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	id := hex.EncodeToString(raw[:])
	snapshot := sample()

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	count := func() int {
		t.Helper()
		var rows int
		if err := sqlDB.QueryRowContext(ctx,
			"SELECT count(*) FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2",
			credentials.AgentID, id).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}

	store.outage.Store(true)
	if err := client.Send(ctx, id, snapshot); status.Code(err) != codes.Unavailable {
		t.Fatalf("injected write outage returned %v", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("unacknowledged write outage inserted %d rows", n)
	}

	store.outage.Store(false)
	store.dropOneACK.Store(true)
	if err := client.Send(ctx, id, snapshot); status.Code(err) != codes.Unavailable {
		t.Fatalf("lost server reply returned %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("lost ACK must follow one committed row, got %d", n)
	}

	stop()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("receiver did not stop after cancellation")
	}
	disconnectedCtx, doneCall := context.WithTimeout(ctx, 400*time.Millisecond)
	err = client.Send(disconnectedCtx, id, snapshot)
	doneCall()
	if err == nil {
		t.Fatal("stopped receiver accepted an RPC")
	}
	if n := count(); n != 1 {
		t.Fatalf("disconnect modified durable row count: %d", n)
	}

	againAddress, stopAgain, doneAgain := start()
	defer stopAgain()
	newClient, err := NewClient(againAddress, credentials, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	if err := newClient.Send(ctx, id, snapshot); err != nil {
		t.Fatalf("retry after restart failed: %v", err)
	}
	if n := count(); n != 1 {
		t.Fatalf("replayed measurement duplicated: %d", n)
	}
	changed := sample()
	changed.CPU.UsedPercent = 98.5
	if err := newClient.Send(ctx, id, changed); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("conflicting payload with same ID accepted: %v", err)
	}
	stopAgain()
	select {
	case err := <-doneAgain:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("restarted receiver did not stop")
	}
}

type chaosBlockingStore struct {
	entered    chan struct{}
	release    chan struct{}
	writes     atomic.Int32
	heartbeats atomic.Int32
}

func (s *chaosBlockingStore) Save(ctx context.Context, _, _ string, _ *pb.Snapshot) (bool, error) {
	s.writes.Add(1)
	select {
	case s.entered <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case <-s.release:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *chaosBlockingStore) RecordHeartbeat(context.Context, string) error {
	s.heartbeats.Add(1)
	return nil
}

// Many authenticated requests compete on a real TLS gRPC transport while
// the first persistence handler is deliberately stalled. The server must
// reject excess work promptly, without starting more Save calls.
func TestChaosGRPCSaturationAndHeartbeatIsolation(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	cert := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	if err := identity.GenerateDevelopmentTLS("127.0.0.1", ca, cert, key); err != nil {
		t.Fatal(err)
	}
	registryFile := filepath.Join(dir, "agents.json")
	credentialsFile := filepath.Join(dir, "client.json")
	if _, err := identity.Provision(registryFile, credentialsFile); err != nil {
		t.Fatal(err)
	}
	registry, err := identity.LoadRegistry(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := identity.LoadCredentials(credentialsFile)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := ServerCredentials(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := &chaosBlockingStore{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- ServeReceiverWithLimits(ctx, listener,
			slog.New(slog.NewTextHandler(io.Discard, nil)), transport, registry, store,
			ReceiverLimits{Measurements: 1, Heartbeats: 1, PerAgent: 1})
	}()
	client, err := NewClient(listener.Addr().String(), credentials, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- client.Send(ctx, fmt.Sprintf("%032x", 1), sample())
	}()
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("first write never occupied the only sample slot")
	}
	const competing = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, competing)
	for i := 0; i < competing; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			results <- client.Send(callCtx, fmt.Sprintf("%032x", i+2), sample())
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("saturated receiver must reject promptly: %v", err)
		}
	}
	if got := store.writes.Load(); got != 1 {
		t.Fatalf("rejected RPCs reached persistence handler: %d", got)
	}
	// Heartbeat capacity is separate; a stalled measurement cannot starve it.
	if err := client.Heartbeat(ctx); err != nil {
		t.Fatalf("heartbeat blocked by saturated measurements: %v", err)
	}
	if store.heartbeats.Load() != 1 {
		t.Fatal("heartbeat was not persisted")
	}
	close(store.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("in-flight request was not released")
	}
	if err := client.Send(ctx, fmt.Sprintf("%032x", competing+2), sample()); err != nil {
		t.Fatalf("capacity not restored after completion: %v", err)
	}
	if got := store.writes.Load(); got != 2 {
		t.Fatalf("unexpected persistence attempt count: %d", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("overloaded receiver failed to shut down")
	}
}
