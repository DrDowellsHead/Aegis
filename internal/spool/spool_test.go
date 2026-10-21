package spool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
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
	if err := q.Close(); err != nil {
		t.Fatal(err)
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
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
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
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := restarted.Stats()
	if before != after {
		t.Fatalf("records were silently evicted: %d -> %d", before, after)
	}
	_ = restarted.Close()
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
	if len(entries) != 2 || r.MeasurementID == "" {
		t.Fatal(entries)
	}
	var p string
	for _, item := range entries {
		if filepath.Ext(item.Name()) == ".json" {
			p = filepath.Join(dir, item.Name())
		}
	}
	if p == "" {
		t.Fatal("record not found")
	}
	if err := os.WriteFile(p, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
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
	_ = q.Close()
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
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := reopened.Peek()
	if !ok || found.MeasurementID != second.MeasurementID {
		t.Fatalf("wrong surviving record: %+v %v", found, ok)
	}
	if err := reopened.Ack(first.MeasurementID); err == nil {
		t.Fatal("ack of missing record unexpectedly succeeded")
	}
	_ = reopened.Close()
}

func TestExclusiveQueueLockAndClosedQueue(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, testAgent, MaxRecordBytes); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open must fail with ErrLocked, got %v", err)
	}
	if _, err := q.Append(snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Append(snapshot()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := q.Ack("missing"); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	reopened, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if count, _ := reopened.Stats(); count != 1 {
		t.Fatalf("lost record: %d", count)
	}
}

func TestQueueLockAcrossProcesses(t *testing.T) {
	if os.Getenv("AEGIS_SPOOL_LOCK_PROBE") == "1" {
		q, err := Open(os.Getenv("AEGIS_SPOOL_LOCK_DIR"), testAgent, MaxRecordBytes)
		if q != nil {
			_ = q.Close()
		}
		if !errors.Is(err, ErrLocked) {
			t.Fatalf("lock not enforced: %v", err)
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "spool")
	q, err := Open(dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestQueueLockAcrossProcesses$")
	child.Env = append(os.Environ(), "AEGIS_SPOOL_LOCK_PROBE=1", "AEGIS_SPOOL_LOCK_DIR="+dir)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

func TestFailedWriteAndDiskReservePreserveRecords(t *testing.T) {
	q, err := Open(filepath.Join(t.TempDir(), "spool"), testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	first, err := q.Append(snapshot())
	if err != nil {
		t.Fatal(err)
	}
	count, used := q.Stats()
	q.spaceCheck = func(string, int64) error { return ErrDiskSpace }
	if _, err := q.Append(snapshot()); !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("low disk: %v", err)
	}
	q.spaceCheck = checkFreeSpace
	q.writeRecord = func(f *os.File, data []byte) error {
		_, _ = f.Write(data[:len(data)/2])
		return syscall.ENOSPC
	}
	if _, err := q.Append(snapshot()); !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("short write: %v", err)
	}
	c, u := q.Stats()
	if c != count || u != used {
		t.Fatalf("failed append changed queue state: %d/%d", c, u)
	}
	r, ok := q.Peek()
	if !ok || r.MeasurementID != first.MeasurementID {
		t.Fatal("original record disappeared")
	}
	files, err := os.ReadDir(q.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name() != ".aegis.lock" && filepath.Ext(f.Name()) != ".json" {
			t.Fatalf("partial file survived: %s", f.Name())
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(q.dir, testAgent, MaxRecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if n, _ := restarted.Stats(); n != 1 {
		t.Fatalf("restart lost measurements: %d", n)
	}
}

func TestRejectSymlinkLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink privileges vary")
	}
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".aegis.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, testAgent, MaxRecordBytes); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
