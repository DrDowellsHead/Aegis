package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
	"github.com/DrDowellsHead/Aegis/internal/spool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const agentID = "123e4567-e89b-42d3-a456-426614174000"

type testCollector struct{ calls int }

func (c *testCollector) Collect(context.Context) (metrics.Snapshot, error) {
	c.calls++
	return metrics.Snapshot{SchemaVersion: 1, StartedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), OS: "linux", Arch: "amd64"}, nil
}

type failThenSend struct {
	mu   sync.Mutex
	ids  []string
	fail int
}

func (s *failThenSend) Send(_ context.Context, r spool.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, r.MeasurementID)
	if len(s.ids) <= s.fail {
		return status.Error(codes.Unavailable, "offline")
	}
	return nil
}
func testOptions(count int) ForwardOptions {
	return ForwardOptions{Interval: 100 * time.Millisecond, Count: count, RequestTimeout: time.Second, RetryMin: 5 * time.Millisecond, RetryMax: 20 * time.Millisecond}
}
func TestRetriesSameMeasurementAndRecoversAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox")
	q, err := spool.Open(path, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	source := &testCollector{}
	sender := &failThenSend{fail: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Forward(ctx, source, sender, q, log, testOptions(1)); err != nil {
		t.Fatal(err)
	}
	if len(sender.ids) != 3 || sender.ids[0] != sender.ids[1] || sender.ids[1] != sender.ids[2] {
		t.Fatalf("retry IDs changed: %v", sender.ids)
	}
	n, _ := q.Stats()
	if n != 0 {
		t.Fatalf("durably acknowledged record remains queued: %d", n)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := spool.Open(path, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	// Start again after the acknowledged sample was removed.
	resumed := &failThenSend{}
	if err := Forward(ctx, source, resumed, restarted, log, testOptions(1)); err != nil {
		t.Fatal(err)
	}
	if len(resumed.ids) != 1 || resumed.ids[0] == sender.ids[0] {
		t.Fatalf("acknowledged record unexpectedly replayed: %v", resumed.ids)
	}
	n, _ = restarted.Stats()
	if n != 0 {
		t.Fatalf("durable ACK did not remove new record: %d", n)
	}
}
func TestAuthenticationErrorIsNotRetried(t *testing.T) {
	q, err := spool.Open(filepath.Join(t.TempDir(), "outbox"), agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	sender := &unauthSender{}
	err = Forward(context.Background(), &testCollector{}, sender, q,
		slog.New(slog.NewTextHandler(io.Discard, nil)), testOptions(1))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("auth error: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("auth retry attempts: %d", sender.calls)
	}
}

type unauthSender struct{ calls int }

func (s *unauthSender) Send(context.Context, spool.Record) error {
	s.calls++
	return status.Error(codes.Unauthenticated, "bad token")
}

func TestCanceledWhileOfflineKeepsOutbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox")
	q, err := spool.Open(path, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	sender := &failThenSend{fail: 100000}
	err = Forward(ctx, &testCollector{}, sender, q, slog.New(slog.NewTextHandler(io.Discard, nil)), testOptions(0))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := q.Stats()
	if n == 0 {
		t.Fatal("lost all offline measurements")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := spool.Open(path, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	m, _ := again.Stats()
	if m != n {
		t.Fatalf("recovery changed %d to %d", n, m)
	}
}

func TestOptionsValidation(t *testing.T) {
	o := testOptions(1)
	o.RetryMin = 0
	q, _ := spool.Open(filepath.Join(t.TempDir(), "outbox"), agentID, spool.MaxRecordBytes)
	err := Forward(context.Background(), &testCollector{}, &failThenSend{}, q, slog.New(slog.NewTextHandler(io.Discard, nil)), o)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("invalid options accepted: %v", err)
	}
}

func TestCancellationInterruptsBackoffAfterCollectionDone(t *testing.T) {
	q, err := spool.Open(filepath.Join(t.TempDir(), "outbox"), agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	opts := testOptions(1)
	opts.RetryMin = 10 * time.Second
	opts.RetryMax = 10 * time.Second
	started := time.Now()
	err = Forward(ctx, &testCollector{}, &failThenSend{fail: 10000}, q,
		slog.New(slog.NewTextHandler(io.Discard, nil)), opts)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancel failed to interrupt retry timer")
	}
	n, _ := q.Stats()
	if n != 1 {
		t.Fatalf("canceled delivery erased queue: %d", n)
	}
}
