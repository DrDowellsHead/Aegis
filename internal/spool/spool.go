// Package spool provides a bounded, restartable on-disk outbox.
// Records are removed only after a durable database acknowledgement.
package spool

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

const MaxRecordBytes int64 = 256 << 10 // 256 KiB per observation

var ErrFull = errors.New("spool is full: measurement not enqueued")
var ErrCorrupt = errors.New("spool contains invalid data")
var ErrLocked = errors.New("spool is already in use")
var ErrClosed = errors.New("spool is closed")
var ErrDiskSpace = errors.New("spool disk space reserve would be exhausted")

// Record is the stable payload and ID used on EVERY retry.
type Record struct {
	AgentID       string           `json:"agent_id"`
	MeasurementID string           `json:"measurement_id"`
	Snapshot      metrics.Snapshot `json:"snapshot"`
}

type entry struct {
	record Record
	size   int64
}

// Queue holds an exclusive OS process lock until Close.
type Queue struct {
	mu          sync.Mutex
	dir         string
	agentID     string
	limit       int64
	used        int64
	entries     map[string]entry // key: on-disk filename
	lock        *os.File
	spaceCheck  func(string, int64) error
	writeRecord func(*os.File, []byte) error
}

func validMeasurementID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validRecord(r Record, agentID string) bool {
	return r.AgentID == agentID && identity.ValidID(r.AgentID) &&
		validMeasurementID(r.MeasurementID) && r.Snapshot.SchemaVersion == 1 &&
		!r.Snapshot.StartedAt.IsZero()
}

// Open recovers complete records and rejects corrupt/mismatched files.
// An abandoned .pending-* temporary file is discarded (it was never enqueued).
func Open(dir, agentID string, maxBytes int64) (queue *Queue, err error) {
	if dir == "" || !identity.ValidID(agentID) || maxBytes < MaxRecordBytes {
		return nil, fmt.Errorf("spool requires directory, valid agent ID and maxBytes >= %d", MaxRecordBytes)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create spool dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("spool directory must be a private regular directory")
	}

	lockPath := filepath.Join(dir, ".aegis.lock")
	if info, statErr := os.Lstat(lockPath); statErr == nil {
		if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("%w: unsafe lock file", ErrCorrupt)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect spool lock: %w", statErr)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open spool lock: %w", err)
	}
	openedInfo, openedErr := lock.Stat()
	pathInfo, pathErr := os.Lstat(lockPath)
	if openedErr != nil || pathErr != nil || !openedInfo.Mode().IsRegular() ||
		!pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) ||
		runtime.GOOS != "windows" && openedInfo.Mode().Perm()&0077 != 0 {
		_ = lock.Close()
		return nil, fmt.Errorf("%w: unsafe lock file", ErrCorrupt)
	}
	if err = lockFile(lock); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("%w: %v", ErrLocked, err)
	}
	defer func() {
		if err != nil {
			_ = unlockFile(lock)
			_ = lock.Close()
		}
	}()
	q := &Queue{dir: dir, agentID: agentID, limit: maxBytes,
		entries: make(map[string]entry), lock: lock,
		spaceCheck: checkFreeSpace, writeRecord: writeRecord}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		name := file.Name()
		full := filepath.Join(dir, name)
		if name == ".aegis.lock" {
			continue
		}
		if strings.HasPrefix(name, ".pending-") {
			if !file.Type().IsRegular() {
				return nil, fmt.Errorf("%w: unsafe temporary file", ErrCorrupt)
			}
			if err := os.Remove(full); err != nil {
				return nil, fmt.Errorf("remove incomplete record: %w", err)
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") || !file.Type().IsRegular() {
			return nil, fmt.Errorf("%w: unexpected spool entry %q", ErrCorrupt, name)
		}
		// Deliberately bounded read; never load arbitrary-sized foreign data.
		info, err := file.Info()
		if err != nil {
			return nil, err
		}
		if info.Size() <= 0 || info.Size() > MaxRecordBytes ||
			runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("%w: unsafe record %q", ErrCorrupt, name)
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read spool record %q: %w", name, err)
		}
		var r Record
		if err := json.Unmarshal(raw, &r); err != nil || !validRecord(r, agentID) || !strings.HasSuffix(name, "_"+r.MeasurementID+".json") {
			return nil, fmt.Errorf("%w: invalid record %q", ErrCorrupt, name)
		}
		if q.used > maxBytes-int64(len(raw)) {
			return nil, fmt.Errorf("%w: existing spool exceeds limit", ErrFull)
		}
		q.entries[name] = entry{record: r, size: int64(len(raw))}
		q.used += int64(len(raw))
	}
	return q, nil
}

// Close releases the OS lock. The lock file must not be deleted.
func (q *Queue) Close() error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.lock == nil {
		return nil
	}
	err := unlockFile(q.lock)
	closeErr := q.lock.Close()
	q.lock = nil
	return errors.Join(err, closeErr)
}

func writeRecord(file *os.File, data []byte) error {
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return syscall.ENOSPC
	}
	return nil
}

func spoolIOError(action string, err error) error {
	if isDiskFull(err) {
		return fmt.Errorf("%s: %w", action, errors.Join(ErrDiskSpace, err))
	}
	return fmt.Errorf("%s: %w", action, err)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate measurement ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Append writes and fsyncs a temporary file before atomically renaming it
// within the same directory. No record is evicted if the quota is exceeded.
// Directory sync is best effort: Windows and some filesystems differ.
func (q *Queue) Append(snapshot metrics.Snapshot) (Record, error) {
	if q == nil {
		return Record{}, fmt.Errorf("spool is nil")
	}
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	rec := Record{AgentID: q.agentID, MeasurementID: id, Snapshot: snapshot}
	if !validRecord(rec, q.agentID) {
		return Record{}, fmt.Errorf("invalid snapshot")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return Record{}, fmt.Errorf("encode spool record: %w", err)
	}
	data = append(data, '\n')
	if int64(len(data)) > MaxRecordBytes {
		return Record{}, fmt.Errorf("measurement exceeds %d bytes", MaxRecordBytes)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.lock == nil {
		return Record{}, ErrClosed
	}
	if q.used > q.limit-int64(len(data)) {
		return Record{}, ErrFull
	}
	if err := q.spaceCheck(q.dir, int64(len(data))); err != nil {
		return Record{}, spoolIOError("check spool free space", err)
	}
	file, err := os.CreateTemp(q.dir, ".pending-")
	if err != nil {
		return Record{}, spoolIOError("create spool temp", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return Record{}, err
	}
	if err := q.writeRecord(file, data); err != nil {
		_ = file.Close()
		return Record{}, spoolIOError("write spool record", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Record{}, spoolIOError("sync spool record", err)
	}
	if err := file.Close(); err != nil {
		return Record{}, spoolIOError("close spool record", err)
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "_" + id + ".json"
	if err := os.Rename(file.Name(), filepath.Join(q.dir, name)); err != nil {
		return Record{}, spoolIOError("commit spool record", err)
	}
	// This sync is not available on every filesystem; file data already synced.
	if directory, err := os.Open(q.dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	q.entries[name] = entry{record: rec, size: int64(len(data))}
	q.used += int64(len(data))
	return rec, nil
}

// List returns an ordered copy; the caller must not mutate its snapshots.
func (q *Queue) List() []Record {
	q.mu.Lock()
	defer q.mu.Unlock()
	names := make([]string, 0, len(q.entries))
	for n := range q.entries {
		names = append(names, n)
	}
	slices.Sort(names)
	result := make([]Record, 0, len(names))
	for _, n := range names {
		result = append(result, q.entries[n].record)
	}
	return result
}

func (q *Queue) Stats() (count int, bytes int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries), q.used
}

// Peek returns the oldest queued record without materializing the whole queue.
func (q *Queue) Peek() (Record, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var first string
	for name := range q.entries {
		if first == "" || name < first {
			first = name
		}
	}
	if first == "" {
		return Record{}, false
	}
	return q.entries[first].record, true
}

// Ack removes an entry only after the caller has verified a durable server ACK.
// If removal fails, it remains queued and will safely be retried.
func (q *Queue) Ack(measurementID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.lock == nil {
		return ErrClosed
	}
	for name, item := range q.entries {
		if item.record.MeasurementID != measurementID {
			continue
		}
		if err := os.Remove(filepath.Join(q.dir, name)); err != nil {
			return fmt.Errorf("remove durably acknowledged record: %w", err)
		}
		delete(q.entries, name)
		q.used -= item.size
		if directory, err := os.Open(q.dir); err == nil {
			_ = directory.Sync() // best effort; replay after crash is safe
			_ = directory.Close()
		}
		return nil
	}
	return fmt.Errorf("measurement ID %s not found in spool", measurementID)
}
