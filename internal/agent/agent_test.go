package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
)

type stub struct {
	calls   int
	partial bool
	err     error
}

func (s *stub) Collect(context.Context) (metrics.Snapshot, error) {
	s.calls++
	m := metrics.Snapshot{SchemaVersion: 1}
	if s.partial {
		m.Errors = map[string]string{"disk": "unavailable"}
	}
	return m, s.err
}
func TestFiniteStream(t *testing.T) {
	var out bytes.Buffer
	s := &stub{}
	if err := Run(context.Background(), s, &out, time.Millisecond, 3); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&out)
	for range 3 {
		var m metrics.Snapshot
		if err := decoder.Decode(&m); err != nil {
			t.Fatal(err)
		}
	}
	var extra metrics.Snapshot
	if err := decoder.Decode(&extra); err != io.EOF || s.calls != 3 {
		t.Fatalf("calls=%d extra=%v", s.calls, err)
	}
}

type cancelWriter struct{ cancel context.CancelFunc }

func (w cancelWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }
func TestCancellationDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &stub{}
	if err := Run(ctx, s, cancelWriter{cancel}, time.Hour, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatalf("calls=%d", s.calls)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestFailures(t *testing.T) {
	s := &stub{}
	if err := Run(context.Background(), s, brokenWriter{}, time.Millisecond, 2); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if s.calls != 1 {
		t.Fatal(s.calls)
	}
	s = &stub{partial: true}
	if err := Run(context.Background(), s, io.Discard, time.Millisecond, 2); !errors.Is(err, ErrPartial) {
		t.Fatal(err)
	}
	if s.calls != 2 {
		t.Fatal(s.calls)
	}
	sentinel := errors.New("source failed")
	if err := Run(context.Background(), &stub{err: sentinel}, io.Discard, time.Millisecond, 1); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
func TestInvalidOptions(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		count    int
	}{{0, 1}, {-1, 1}, {time.Hour, -1}, {25 * time.Hour, 1}} {
		s := &stub{}
		if err := Run(context.Background(), s, io.Discard, tc.interval, tc.count); err == nil || s.calls != 0 {
			t.Fatalf("%+v err=%v calls=%d", tc, err, s.calls)
		}
	}
}
