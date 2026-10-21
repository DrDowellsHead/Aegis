package storage

import (
	"context"
	"fmt"
	"math"

	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	"google.golang.org/protobuf/proto"
)

func percent(v float64) (any, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
		return nil, fmt.Errorf("percentage outside 0..100")
	}
	return v, nil
}

// project converts absent groups into SQL NULL rather than fabricated zeroes.
func project(s *pb.Snapshot) (cpu, memory, disk, path any, err error) {
	if s == nil {
		return nil, nil, nil, nil, fmt.Errorf("snapshot is nil")
	}
	if s.Cpu != nil {
		cpu, err = percent(s.Cpu.UsedPercent)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("CPU: %w", err)
		}
	}
	if s.Memory != nil {
		memory, err = percent(s.Memory.UsedPercent)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("memory: %w", err)
		}
	}
	if s.Disk != nil {
		disk, err = percent(s.Disk.UsedPercent)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("disk: %w", err)
		}
		path = s.Disk.Path
	}
	return cpu, memory, disk, path, nil
}

type backfillRow struct {
	agentID       string
	measurementID string
	payload       []byte
}

// BackfillBatch fills at most limit legacy rows in one transaction, atomically
// marking each row projected. Interrupted batches roll back completely.
// This method requires write access; the read-only history role cannot call it.
func (p *Postgres) BackfillBatch(ctx context.Context, limit int) (int, error) {
	return p.backfillBatch(ctx, limit, "")
}

// BackfillBatchAudited updates progress in the same transaction as its rows.
func (p *Postgres) BackfillBatchAudited(ctx context.Context, limit int, auditID string) (int, error) {
	if !validStoredAgentID(auditID) {
		return 0, fmt.Errorf("invalid audit ID")
	}
	return p.backfillBatch(ctx, limit, auditID)
}

func (p *Postgres) backfillBatch(ctx context.Context, limit int, auditID string) (int, error) {
	if p == nil || p.db == nil {
		return 0, fmt.Errorf("database unavailable")
	}
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("batch size must be 1..500")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin backfill: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT agent_id::text, measurement_id, payload
		FROM aegis_measurements
		WHERE projected = FALSE
		ORDER BY agent_id, measurement_id
		LIMIT $1 FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("load unprojected rows: %w", err)
	}
	batch := make([]backfillRow, 0, limit)
	for rows.Next() {
		var r backfillRow
		if err := rows.Scan(&r.agentID, &r.measurementID, &r.payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan unprojected row: %w", err)
		}
		batch = append(batch, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("read unprojected rows: %w", err)
	}
	for _, r := range batch {
		var snapshot pb.Snapshot
		if err := proto.Unmarshal(r.payload, &snapshot); err != nil {
			return 0, fmt.Errorf("decode legacy row %s/%s: %w", r.agentID, r.measurementID, err)
		}
		cpu, memory, disk, path, err := project(&snapshot)
		if err != nil {
			return 0, fmt.Errorf("project legacy row %s/%s: %w", r.agentID, r.measurementID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE aegis_measurements
			SET cpu_used_percent=$3, memory_used_percent=$4,
			    disk_used_percent=$5, disk_path=$6, projected=TRUE
			WHERE agent_id=$1 AND measurement_id=$2 AND projected=FALSE
		`, r.agentID, r.measurementID, cpu, memory, disk, path); err != nil {
			return 0, fmt.Errorf("backfill row %s/%s: %w", r.agentID, r.measurementID, err)
		}
	}
	if err := advanceAudit(ctx, tx, auditID, int64(len(batch))); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit backfill: %w", err)
	}
	return len(batch), nil
}
