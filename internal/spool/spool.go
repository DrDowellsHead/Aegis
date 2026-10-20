// Package spool provides a bounded, restartable on-disk outbox.
// Records are NEVER removed on a volatile server receipt.
package spool

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

const MaxRecordBytes int64 = 256 << 10 // 256 KiB per observation

var ErrFull = errors.New("spool is full: measurement not enqueued")
var ErrCorrupt = errors.New("spool contains invalid data")

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

// Queue has one owning process. Multiple Queue instances for the same
// directory are unsupported; a filesystem process lock is not yet available.
type Queue struct {
	mu      sync.Mutex
	dir     string
	agentID string
	limit   int64
	used    int64
	entries map[string]entry // key: on-disk filename
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
func Open(dir, agentID string, maxBytes int64) (*Queue, error) {
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
	q := &Queue{dir: dir, agentID: agentID, limit: maxBytes, entries: make(map[string]entry)}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		name := file.Name()
		full := filepath.Join(dir, name)
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
	if q.used > q.limit-int64(len(data)) {
		return Record{}, ErrFull
	}
	file, err := os.CreateTemp(q.dir, ".pending-")
	if err != nil {
		return Record{}, fmt.Errorf("create spool temp: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return Record{}, err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return Record{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Record{}, err
	}
	if err := file.Close(); err != nil {
		return Record{}, err
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "_" + id + ".json"
	if err := os.Rename(file.Name(), filepath.Join(q.dir, name)); err != nil {
		return Record{}, fmt.Errorf("commit spool record: %w", err)
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

// No Delete or ACK method yet: volatile received=true is NOT durable.
// The server's durable acknowledgement is introduced in step 3.4.
var _ io.Reader // keep fsync/rename disk contract explicit in the package
