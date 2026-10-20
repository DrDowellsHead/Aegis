package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/DrDowellsHead/Aegis/internal/metrics"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/proto"
)

// HistoryQuery uses an exclusive keyset cursor sorted by (started_at, measurement_id).
// From is inclusive, To exclusive. Limit must be between 1 and 100.
type HistoryQuery struct {
	AgentID    string
	From       time.Time
	To         time.Time
	Limit      int
	CursorTime time.Time
	CursorID   string
}

// HistoryItem is a reconstructed, human-readable snapshot.
type HistoryItem struct {
	AgentID       string           `json:"agent_id"`
	MeasurementID string           `json:"measurement_id"`
	StartedAt     time.Time        `json:"started_at"`
	ReceivedAt    time.Time        `json:"received_at"`
	Snapshot      metrics.Snapshot `json:"snapshot"`
}

type HistoryResult struct {
	Items   []HistoryItem
	HasMore bool
}

const listHistorySQL = `
SELECT measurement_id, started_at, received_at, payload
FROM aegis_measurements
WHERE agent_id = $1
  AND ($2::timestamptz IS NULL OR started_at >= $2::timestamptz)
  AND ($3::timestamptz IS NULL OR started_at < $3::timestamptz)
  AND ($4::timestamptz IS NULL OR
       (started_at, measurement_id) < ($4::timestamptz, $5::text))
ORDER BY started_at DESC, measurement_id DESC
LIMIT $6
`

// ListHistory performs bounded keyset pagination without exposing database rows.
// The result is not a point-in-time snapshot across separate HTTP requests.
func (p *Postgres) ListHistory(ctx context.Context, q HistoryQuery) (HistoryResult, error) {
	if p == nil || p.db == nil {
		return HistoryResult{}, fmt.Errorf("database not initialized")
	}
	if !identity.ValidID(q.AgentID) || q.Limit < 1 || q.Limit > 100 ||
		(!q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To)) ||
		(q.CursorTime.IsZero() != (q.CursorID == "")) {
		return HistoryResult{}, fmt.Errorf("invalid history query")
	}
	if q.CursorID != "" && !validStoredMeasurementID(q.CursorID) {
		return HistoryResult{}, fmt.Errorf("invalid history cursor")
	}
	var from, to, cursor any
	if !q.From.IsZero() {
		from = q.From
	}
	if !q.To.IsZero() {
		to = q.To
	}
	if !q.CursorTime.IsZero() {
		cursor = q.CursorTime
	}
	rows, err := p.db.QueryContext(ctx, listHistorySQL,
		q.AgentID, from, to, cursor, q.CursorID, q.Limit+1)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()
	out := HistoryResult{Items: make([]HistoryItem, 0, q.Limit)}
	for rows.Next() {
		var id string
		var started, received time.Time
		var raw []byte
		if err := rows.Scan(&id, &started, &received, &raw); err != nil {
			return HistoryResult{}, fmt.Errorf("scan measurement: %w", err)
		}
		if len(out.Items) == q.Limit {
			out.HasMore = true
			break
		}
		var wire pb.Snapshot
		if err := proto.Unmarshal(raw, &wire); err != nil {
			return HistoryResult{}, fmt.Errorf("decode stored protobuf: %w", err)
		}
		snapshot, err := decodeHistorySnapshot(&wire)
		if err != nil {
			return HistoryResult{}, err
		}
		out.Items = append(out.Items, HistoryItem{
			AgentID: q.AgentID, MeasurementID: id,
			StartedAt: started.UTC(), ReceivedAt: received.UTC(),
			Snapshot: snapshot,
		})
	}
	if err := rows.Err(); err != nil {
		return HistoryResult{}, fmt.Errorf("iterate measurements: %w", err)
	}
	return out, nil
}

func validStoredMeasurementID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, b := range id {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func decodeHistorySnapshot(s *pb.Snapshot) (metrics.Snapshot, error) {
	if s == nil || s.StartedAt == nil {
		return metrics.Snapshot{}, fmt.Errorf("stored measurement has no timestamp")
	}
	if err := s.StartedAt.CheckValid(); err != nil {
		return metrics.Snapshot{}, fmt.Errorf("stored measurement has invalid timestamp: %w", err)
	}
	out := metrics.Snapshot{
		SchemaVersion: int(s.SchemaVersion),
		StartedAt:     s.StartedAt.AsTime().UTC(),
		DurationMS:    s.DurationMs,
		OS:            s.Os, Arch: s.Arch,
	}
	if s.Cpu != nil {
		out.CPU = &metrics.CPU{UsedPercent: s.Cpu.UsedPercent}
	}
	if s.Memory != nil {
		out.Memory = &metrics.Memory{
			TotalBytes: s.Memory.TotalBytes, AvailableBytes: s.Memory.AvailableBytes,
			UsedBytes: s.Memory.UsedBytes, UsedPercent: s.Memory.UsedPercent,
		}
	}
	if s.Disk != nil {
		out.Disk = &metrics.Disk{
			Path: s.Disk.Path, TotalBytes: s.Disk.TotalBytes,
			FreeBytes: s.Disk.FreeBytes, UsedBytes: s.Disk.UsedBytes,
			UsedPercent: s.Disk.UsedPercent,
		}
	}
	if s.NetworkAvailable {
		out.Network = make([]metrics.Interface, 0, len(s.Network))
		for _, n := range s.Network {
			if n == nil {
				return metrics.Snapshot{}, fmt.Errorf("stored network interface is nil")
			}
			out.Network = append(out.Network, metrics.Interface{
				Name: n.Name, BytesSent: n.BytesSent, BytesReceived: n.BytesReceived,
			})
		}
	}
	if len(s.Errors) != 0 {
		out.Errors = make(map[string]string, len(s.Errors))
		for k, v := range s.Errors {
			out.Errors[k] = v
		}
	}
	return out, nil
}
