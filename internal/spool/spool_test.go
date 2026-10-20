package spool

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

const testAgent = "123e4567-e89b-42d3-a456-426614174000"
const otherAgent = "123e4567-e89b-42d3-b456-426614174000"

func snapshot() metrics.Snapshot {
	return metrics.Snapshot{
		SchemaVersion: 1, StartedAt: time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC),
		OS: "linux", Arch: "amd64", CPU: &metrics.CPU{UsedPercent: 0},
		Errors: map[string]string{"disk": "unavailable"},
	}
}
func TestRestartPreservesIDsAndPartialMetrics(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	r, err := q.Append(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.MeasurementID) != 32 {
		t.Fatal(r.MeasurementID)
	}
	restarted, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	records := restarted.List()
	if len(records) != 1 || records[0].MeasurementID != r.MeasurementID ||
		records[0].Snapshot.CPU == nil || records[0].Snapshot.Errors["disk"] != "unavailable" {
		t.Fatalf("recovery: %+v", records)
	}
	if _, err := Open(dir, otherAgent, MaxRecordBytes); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("agent identity mismatch allowed: %v", err)
	}
}
func TestBoundedQueueNeverEvicts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	filled := false
	for i := 0; i < 2000; i++ {
		if _, err := q.Append(snapshot()); errors.Is(err, ErrFull) {
			filled = true
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if !filled {
		t.Fatal("expected bounded queue to fill")
	}
	before, _ := q.Stats()
	restarted, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := restarted.Stats()
	if before != after {
		t.Fatalf("records were silently evicted: %d -> %d", before, after)
	}
}
func TestCorruptionIsNotSilentlyDropped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	r, err := q.Append(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || r.MeasurementID == "" {
		t.Fatal(entries)
	}
	p := filepath.Join(dir, entries[0].Name())
	if err := os.WriteFile(p, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, testAgent, MaxRecordBytes); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corruption accepted: %v", err)
	}
}
func TestIncompleteTemporaryFileIsDiscarded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pending-abandoned"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	count, _ := q.Stats()
	if count != 0 {
		t.Fatal(count)
	}
}
func TestRejectsUnsafeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions handled by Windows ACL")
	}
	dir := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, testAgent, MaxRecordBytes); err == nil {
		t.Fatal("accepted public spool directory")
	}
}

func TestAckDeletesOnlyConfirmedRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	first, err := q.Append(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	second, err := q.Append(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Ack(first.MeasurementID); err != nil {
		t.Fatal(err)
	}
	n, used := q.Stats()
	if n != 1 || used <= 0 {
		t.Fatalf("count=%d bytes=%d", n, used)
	}
	reopened, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := reopened.Peek()
	if !ok || found.MeasurementID != second.MeasurementID {
		t.Fatalf("wrong surviving record: %+v %v", found, ok)
	}
	if err := q.Ack(first.MeasurementID); err == nil {
		t.Fatal("ack of missing record unexpectedly succeeded")
	}
}
