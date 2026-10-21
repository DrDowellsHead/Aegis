package agent

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/spool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This fake commits a record and then loses the response once. Its durable
// record map is intentionally separate from the spool's acknowledgement.
type chaosLostACKSender struct {
	committed map[string]spool.Record
	attempts  []string
}

func (s *chaosLostACKSender) Send(_ context.Context, r spool.Record) error {
	s.attempts = append(s.attempts, r.MeasurementID)
	if _, ok := s.committed[r.MeasurementID]; !ok {
		s.committed[r.MeasurementID] = r
		return status.Error(codes.Unavailable, "injected lost acknowledgement after commit")
	}
	return nil
}

func TestChaosLostAcknowledgementReplaysStableID(t *testing.T) {
	q, err := spool.Open(filepath.Join(t.TempDir(), "outbox"), agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	sender := &chaosLostACKSender{committed: make(map[string]spool.Record)}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Forward(ctx, &testCollector{}, sender, q, logger, testOptions(1)); err != nil {
		t.Fatal(err)
	}
	if len(sender.attempts) != 2 || sender.attempts[0] != sender.attempts[1] {
		t.Fatalf("retries did not preserve the stable ID: %v", sender.attempts)
	}
	if len(sender.committed) != 1 {
		t.Fatalf("lost ACK caused duplicate persistent records: %d", len(sender.committed))
	}
	if count, _ := q.Stats(); count != 0 {
		t.Fatalf("spool did not drain after durable retry: %d", count)
	}
}

func TestChaosOfflineShutdownAndSpoolRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbox")
	q, err := spool.Open(dir, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	if err := Forward(ctx, &testCollector{}, &failThenSend{fail: 100000},
		q, logger, testOptions(0)); err != nil {
		t.Fatal(err)
	}
	pending := q.List()
	if len(pending) == 0 {
		t.Fatal("network failure erased the unacknowledged outbox")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := spool.Open(dir, agentID, spool.MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if count, _ := again.Stats(); count != len(pending) {
		t.Fatalf("restart changed pending records: %d -> %d", len(pending), count)
	}
	resumed := &failThenSend{}
	recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer recoverCancel()
	if err := Forward(recoverCtx, &testCollector{}, resumed, again, logger, testOptions(1)); err != nil {
		t.Fatal(err)
	}
	if len(resumed.ids) < len(pending)+1 {
		t.Fatalf("not all saved records were transmitted: %v", resumed.ids)
	}
	for i, r := range pending {
		if resumed.ids[i] != r.MeasurementID {
			t.Fatalf("recovered record %d changed its ID: got %s want %s", i, resumed.ids[i], r.MeasurementID)
		}
	}
	if count, _ := again.Stats(); count != 0 {
		t.Fatalf("replayed outbox not drained: %d", count)
	}
}
