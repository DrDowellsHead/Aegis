package metrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSource struct {
	diskErr error
	gate    chan struct{}
	started chan struct{}
}

func (f fakeSource) wait(ctx context.Context) error {
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.gate == nil {
		return nil
	}
	select {
	case <-f.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f fakeSource) CPU(ctx context.Context) (*CPU, error) {
	err := f.wait(ctx)
	return &CPU{UsedPercent: 25}, err
}
func (f fakeSource) Memory(ctx context.Context) (*Memory, error) {
	err := f.wait(ctx)
	return &Memory{TotalBytes: 1024}, err
}
func (f fakeSource) Disk(ctx context.Context) (*Disk, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &Disk{TotalBytes: 2048}, f.diskErr
}
func (f fakeSource) Network(ctx context.Context) ([]Interface, error) {
	err := f.wait(ctx)
	return []Interface{{Name: "test"}}, err
}

func TestPartialFailure(t *testing.T) {
	c, _ := New(fakeSource{diskErr: errors.New("disk unavailable")}, time.Second)
	s, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU == nil || s.Memory == nil || len(s.Network) != 1 || s.Disk != nil || s.Errors["disk"] != "disk unavailable" {
		t.Fatalf("%+v", s)
	}
}

func TestWorkersRunConcurrentlyAndJoin(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	c, _ := New(fakeSource{gate: gate, started: started}, time.Second)
	done := make(chan error, 1)
	go func() { _, err := c.Collect(context.Background()); done <- err }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("worker not started")
		}
	}
	close(gate)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("workers not joined")
	}
}

func TestDeadlineAndCancellation(t *testing.T) {
	c, _ := New(fakeSource{gate: make(chan struct{})}, 20*time.Millisecond)
	s, err := c.Collect(context.Background())
	if err != nil || len(s.Errors) != 4 || s.CPU != nil {
		t.Fatalf("%+v %v", s, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Collect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeCollection(t *testing.T) {
	source, err := NewSystem(t.TempDir(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := New(source, 10*time.Second)
	s, err := c.Collect(context.Background())
	if err != nil || len(s.Errors) != 0 {
		t.Fatalf("native collection failed: %+v %v", s, err)
	}
	if s.CPU == nil || s.Memory == nil || s.Disk == nil || s.Memory.TotalBytes == 0 || s.Disk.TotalBytes == 0 {
		t.Fatalf("missing metrics: %+v", s)
	}
}
