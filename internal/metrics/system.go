package metrics

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// System reads local OS counters. In containers these can have mixed scopes;
// it does not claim to resolve cgroup limits or observe the physical host.
type System struct {
	path   string
	window time.Duration
}

func NewSystem(path string, window time.Duration) (*System, error) {
	if path == "" {
		return nil, fmt.Errorf("disk path is required")
	}
	if window < 100*time.Millisecond || window > 10*time.Second {
		return nil, fmt.Errorf("CPU window must be between 100ms and 10s")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve disk path: %w", err)
	}
	return &System{path: absolute, window: window}, nil
}

func (s *System) CPU(ctx context.Context) (*CPU, error) {
	values, err := cpu.PercentWithContext(ctx, s.window, false)
	if err != nil {
		return nil, fmt.Errorf("read CPU: %w", err)
	}
	if len(values) != 1 || math.IsNaN(values[0]) || math.IsInf(values[0], 0) || values[0] < 0 || values[0] > 100 {
		return nil, fmt.Errorf("invalid aggregate CPU measurement")
	}
	return &CPU{UsedPercent: values[0]}, nil
}
func (s *System) Memory(ctx context.Context) (*Memory, error) {
	v, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("read memory: %w", err)
	}
	return &Memory{TotalBytes: v.Total, AvailableBytes: v.Available, UsedBytes: v.Used, UsedPercent: v.UsedPercent}, nil
}
func (s *System) Disk(ctx context.Context) (*Disk, error) {
	v, err := disk.UsageWithContext(ctx, s.path)
	if err != nil {
		return nil, fmt.Errorf("read disk: %w", err)
	}
	return &Disk{Path: s.path, TotalBytes: v.Total, FreeBytes: v.Free, UsedBytes: v.Used, UsedPercent: v.UsedPercent}, nil
}
func (s *System) Network(ctx context.Context) ([]Interface, error) {
	values, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("read network: %w", err)
	}
	result := make([]Interface, 0, len(values))
	for _, v := range values {
		result = append(result, Interface{Name: v.Name, BytesSent: v.BytesSent, BytesReceived: v.BytesRecv})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
