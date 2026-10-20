// Package metrics collects a bounded set of measurements visible to this process.
package metrics

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
)

type CPU struct {
	UsedPercent float64 `json:"used_percent"`
}
type Memory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}
type Disk struct {
	Path        string  `json:"path"`
	TotalBytes  uint64  `json:"total_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}
type Interface struct {
	Name          string `json:"name"`
	BytesSent     uint64 `json:"bytes_sent"`
	BytesReceived uint64 `json:"bytes_received"`
}

// Snapshot is an observation window, not an atomic snapshot of the OS.
// A nil measurement and an errors entry denote missing data, never a fake zero.
type Snapshot struct {
	SchemaVersion int               `json:"schema_version"`
	StartedAt     time.Time         `json:"started_at"`
	DurationMS    int64             `json:"duration_ms"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	CPU           *CPU              `json:"cpu"`
	Memory        *Memory           `json:"memory"`
	Disk          *Disk             `json:"disk"`
	Network       []Interface       `json:"network"`
	Errors        map[string]string `json:"errors,omitempty"`
}

// Source is the OS boundary consumed by Collector. Methods must be safe to call
// concurrently. They should honor context, subject to native syscall limitations.
type Source interface {
	CPU(context.Context) (*CPU, error)
	Memory(context.Context) (*Memory, error)
	Disk(context.Context) (*Disk, error)
	Network(context.Context) ([]Interface, error)
}

type Collector struct {
	source  Source
	timeout time.Duration
}

func New(source Source, timeout time.Duration) (*Collector, error) {
	if source == nil {
		return nil, fmt.Errorf("metrics source is required")
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, fmt.Errorf("collection timeout must be in (0, 1m]")
	}
	return &Collector{source: source, timeout: timeout}, nil
}

// Collect joins all four workers before returning. No workers survive a sample.
// Context is cooperative: it cannot forcibly interrupt every native OS call.
func (c *Collector) Collect(parent context.Context) (Snapshot, error) {
	if err := parent.Err(); err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	start := time.Now()
	s := Snapshot{SchemaVersion: 1, StartedAt: start.UTC(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	var errs [4]error
	var wg sync.WaitGroup
	wg.Add(4)
	// Each worker exclusively owns one field and one error slot. Wait synchronizes
	// completion before the caller reads the fields or serializes the snapshot.
	go func() { defer wg.Done(); s.CPU, errs[0] = c.source.CPU(ctx) }()
	go func() { defer wg.Done(); s.Memory, errs[1] = c.source.Memory(ctx) }()
	go func() { defer wg.Done(); s.Disk, errs[2] = c.source.Disk(ctx) }()
	go func() { defer wg.Done(); s.Network, errs[3] = c.source.Network(ctx) }()
	wg.Wait()
	if err := parent.Err(); err != nil {
		return Snapshot{}, err
	}
	s.DurationMS = time.Since(start).Milliseconds()
	for i, name := range []string{"cpu", "memory", "disk", "network"} {
		if errs[i] != nil {
			if s.Errors == nil {
				s.Errors = make(map[string]string)
			}
			s.Errors[name] = errs[i].Error()
		}
	}
	if errs[0] != nil {
		s.CPU = nil
	}
	if errs[1] != nil {
		s.Memory = nil
	}
	if errs[2] != nil {
		s.Disk = nil
	}
	if errs[3] != nil {
		s.Network = nil
	}
	return s, nil
}
