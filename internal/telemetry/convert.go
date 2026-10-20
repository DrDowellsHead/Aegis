// Package telemetry translates local snapshots to the versioned wire contract.
package telemetry

import (
	"fmt"
	"math"

	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func EncodeSnapshot(s metrics.Snapshot) (*pb.Snapshot, error) {
	if s.SchemaVersion != 1 || s.StartedAt.IsZero() || s.DurationMS < 0 {
		return nil, fmt.Errorf("unsupported or invalid metrics snapshot")
	}
	result := &pb.Snapshot{
		SchemaVersion:    uint32(s.SchemaVersion),
		StartedAt:        timestamppb.New(s.StartedAt),
		DurationMs:       s.DurationMS,
		Os:               s.OS,
		Arch:             s.Arch,
		NetworkAvailable: s.Network != nil,
	}
	if err := result.StartedAt.CheckValid(); err != nil {
		return nil, fmt.Errorf("invalid measurement timestamp: %w", err)
	}
	if s.CPU != nil {
		result.Cpu = &pb.CPU{UsedPercent: s.CPU.UsedPercent}
	}
	if s.Memory != nil {
		result.Memory = &pb.Memory{
			TotalBytes: s.Memory.TotalBytes, AvailableBytes: s.Memory.AvailableBytes,
			UsedBytes: s.Memory.UsedBytes, UsedPercent: s.Memory.UsedPercent,
		}
	}
	if s.Disk != nil {
		result.Disk = &pb.Disk{
			Path: s.Disk.Path, TotalBytes: s.Disk.TotalBytes,
			FreeBytes: s.Disk.FreeBytes, UsedBytes: s.Disk.UsedBytes,
			UsedPercent: s.Disk.UsedPercent,
		}
	}
	if s.Network != nil {
		result.Network = make([]*pb.NetworkInterface, 0, len(s.Network))
		for _, n := range s.Network {
			result.Network = append(result.Network, &pb.NetworkInterface{
				Name: n.Name, BytesSent: n.BytesSent, BytesReceived: n.BytesReceived,
			})
		}
	}
	if len(s.Errors) > 0 {
		result.Errors = make(map[string]string, len(s.Errors))
		for k, v := range s.Errors {
			result.Errors[k] = v
		}
	}
	return result, nil
}

func ValidateSnapshot(s *pb.Snapshot) error {
	if s == nil || s.GetSchemaVersion() != 1 || s.GetStartedAt() == nil ||
		s.GetDurationMs() < 0 || s.GetOs() == "" || s.GetArch() == "" {
		return fmt.Errorf("missing fields or unsupported snapshot version")
	}
	if err := s.GetStartedAt().CheckValid(); err != nil {
		return fmt.Errorf("invalid started_at: %w", err)
	}
	if s.Cpu != nil && invalidPercent(s.Cpu.UsedPercent) {
		return fmt.Errorf("invalid CPU percentage")
	}
	if s.Memory != nil && invalidPercent(s.Memory.UsedPercent) {
		return fmt.Errorf("invalid memory percentage")
	}
	if s.Disk != nil && invalidPercent(s.Disk.UsedPercent) {
		return fmt.Errorf("invalid disk percentage")
	}
	if !s.NetworkAvailable && len(s.Network) != 0 {
		return fmt.Errorf("network entries without available network group")
	}
	for k, v := range s.Errors {
		if k == "" || v == "" {
			return fmt.Errorf("empty metrics error key or value")
		}
	}
	return nil
}

func invalidPercent(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100
}
