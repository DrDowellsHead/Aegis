package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

const (
	MinRetentionDays  = 7
	MaxRetentionDays  = 3650
	MaxRetentionBatch = 2000
)

// StorageUsage reports actual PostgreSQL relation sizes in bytes, not
// a prediction of how much disk space DELETE will release to the OS.
type StorageUsage struct {
	CheckedAt             time.Time `json:"checked_at"`
	MeasurementRows       int64     `json:"measurement_rows"`
	MeasurementTableBytes int64     `json:"measurement_table_bytes"`
	MeasurementIndexBytes int64     `json:"measurement_index_bytes"`
	MeasurementTotalBytes int64     `json:"measurement_total_bytes"`
	DatabaseTotalBytes    int64     `json:"database_total_bytes"`
	EstimatedDeadRows     int64     `json:"estimated_dead_rows"`
}

// RetentionFilter optionally restricts a policy to one registered or
// historical agent. Empty AgentID means an all-agent preview.
type RetentionFilter struct {
	Days    int
	AgentID string
}

func (f RetentionFilter) Validate() error {
	if f.Days < MinRetentionDays || f.Days > MaxRetentionDays {
		return fmt.Errorf("retention days must be between %d and %d", MinRetentionDays, MaxRetentionDays)
	}
	if f.AgentID != "" && !identity.ValidID(f.AgentID) {
		return fmt.Errorf("invalid agent ID")
	}
	return nil
}

type RetentionPreview struct {
	CheckedAt     time.Time `json:"checked_at"`
	Cutoff        time.Time `json:"cutoff"`
	RetentionDays int       `json:"retention_days"`
	AgentID       string    `json:"agent_id,omitempty"`
	EligibleRows  int64     `json:"eligible_rows"`
}

// Usage queries PostgreSQL's storage accounting functions. Indexes, TOAST
// and dead tuples are included in total relation size; database size
// includes other relations, WAL is not covered by pg_database_size.
func (p *Postgres) Usage(ctx context.Context) (StorageUsage, error) {
	if p == nil || p.db == nil {
		return StorageUsage{}, fmt.Errorf("database unavailable")
	}
	var u StorageUsage
	err := p.db.QueryRowContext(ctx, `
		SELECT clock_timestamp(),
		       (SELECT count(*) FROM aegis_measurements),
		       pg_relation_size('aegis_measurements'::regclass),
		       pg_indexes_size('aegis_measurements'::regclass),
		       pg_total_relation_size('aegis_measurements'::regclass),
		       pg_database_size(current_database()),
		       COALESCE((SELECT n_dead_tup FROM pg_stat_user_tables
		                 WHERE relid='aegis_measurements'::regclass),0)
	`).Scan(&u.CheckedAt, &u.MeasurementRows, &u.MeasurementTableBytes,
		&u.MeasurementIndexBytes, &u.MeasurementTotalBytes,
		&u.DatabaseTotalBytes, &u.EstimatedDeadRows)
	if err != nil {
		return StorageUsage{}, fmt.Errorf("read PostgreSQL storage usage: %w", err)
	}
	u.CheckedAt = u.CheckedAt.UTC()
	return u, nil
}

func agentArgument(agentID string) any {
	if agentID == "" {
		return nil
	}
	return agentID
}

// Preview anchors the cutoff to PostgreSQL time, not to the machine running
// the operator CLI. Existing data is not modified.
func (p *Postgres) Preview(ctx context.Context, f RetentionFilter) (RetentionPreview, error) {
	if err := f.Validate(); err != nil {
		return RetentionPreview{}, err
	}
	if p == nil || p.db == nil {
		return RetentionPreview{}, fmt.Errorf("database unavailable")
	}
	var result RetentionPreview
	result.RetentionDays = f.Days
	result.AgentID = f.AgentID
	if err := p.db.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&result.CheckedAt); err != nil {
		return RetentionPreview{}, fmt.Errorf("read PostgreSQL clock: %w", err)
	}
	result.CheckedAt = result.CheckedAt.UTC()
	result.Cutoff = result.CheckedAt.Add(-time.Duration(f.Days) * 24 * time.Hour)
	if err := p.db.QueryRowContext(ctx, `
		SELECT count(*) FROM aegis_measurements
		WHERE received_at < $1
		  AND ($2::uuid IS NULL OR agent_id=$2::uuid)
	`, result.Cutoff, agentArgument(f.AgentID)).Scan(&result.EligibleRows); err != nil {
		return RetentionPreview{}, fmt.Errorf("preview expired measurements: %w", err)
	}
	return result, nil
}

// DeleteExpiredBatch safely processes one transaction without operator audit.
func (p *Postgres) DeleteExpiredBatch(ctx context.Context, cutoff time.Time, agentID string, batchSize int) (int64, error) {
    return p.deleteExpiredBatch(ctx,cutoff,agentID,batchSize,"")
}

// deleteExpiredBatch updates audit progress within the SAME transaction as
// DELETE. Neither the progress counter nor the deletion can commit alone.
func(p *Postgres)deleteExpiredBatch(ctx context.Context,cutoff time.Time,agentID string,batchSize int,auditID string)(int64,error){
    if p==nil||p.db==nil{return 0,fmt.Errorf("database unavailable")}
    if cutoff.IsZero()||batchSize<1||batchSize>MaxRetentionBatch||
       (agentID!=""&&!identity.ValidID(agentID)) {return 0,fmt.Errorf("invalid retention cutoff, batch size or agent")}
    tx,err:=p.db.BeginTx(ctx,nil)
    if err!=nil{return 0,fmt.Errorf("begin retention transaction: %w",err)}
    defer tx.Rollback()
    var deleted int64
    err=tx.QueryRowContext(ctx,`
        WITH candidates AS (
            SELECT agent_id, measurement_id FROM aegis_measurements
            WHERE received_at < $1
              AND ($2::uuid IS NULL OR agent_id=$2::uuid)
            ORDER BY received_at,agent_id,measurement_id
            LIMIT $3 FOR UPDATE SKIP LOCKED
        ), deleted AS (
            DELETE FROM aegis_measurements m USING candidates c
            WHERE m.agent_id=c.agent_id AND m.measurement_id=c.measurement_id
            RETURNING 1
        )
        SELECT count(*) FROM deleted
    `,cutoff,agentArgument(agentID),batchSize).Scan(&deleted)
    if err!=nil{return 0,fmt.Errorf("delete expired measurements: %w",err)}
    if err:=advanceAudit(ctx,tx,auditID,deleted);err!=nil{return 0,err}
    if err:=tx.Commit();err!=nil{return 0,fmt.Errorf("commit audited retention: %w",err)}
    return deleted,nil
}

// Purge is explicitly bounded. A nonzero error reports how many earlier
// batches already committed; operators can safely resume the same policy.
func (p *Postgres) Purge(ctx context.Context, preview RetentionPreview, batchSize, maxDelete int) (int64, error) {
    return p.purge(ctx,preview,batchSize,maxDelete,"")
}

// PurgeAudited records per-batch progress durably with each DELETE.
func(p *Postgres)PurgeAudited(ctx context.Context,preview RetentionPreview,batchSize,maxDelete int,auditID string)(int64,error){
    if !validStoredAgentID(auditID){return 0,fmt.Errorf("invalid audit ID")}
    return p.purge(ctx,preview,batchSize,maxDelete,auditID)
}

func(p *Postgres)purge(ctx context.Context,preview RetentionPreview,batchSize,maxDelete int,auditID string)(int64,error){
	if err := (RetentionFilter{Days: preview.RetentionDays, AgentID: preview.AgentID}).Validate(); err != nil {
		return 0, err
	}
	if preview.CheckedAt.IsZero() || preview.Cutoff.IsZero() ||
		!preview.Cutoff.Equal(preview.CheckedAt.Add(-time.Duration(preview.RetentionDays)*24*time.Hour)) ||
		preview.Cutoff.After(time.Now().Add(time.Minute)) ||
		batchSize < 1 || batchSize > MaxRetentionBatch || maxDelete < 1 || maxDelete > 1_000_000 {
		return 0, fmt.Errorf("invalid retention execution limits or cutoff")
	}
	var total int64
	for total < int64(maxDelete) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n := batchSize
		if remain := int64(maxDelete) - total; remain < int64(n) {
			n = int(remain)
		}
		deleted, err := p.deleteExpiredBatch(ctx, preview.Cutoff, preview.AgentID, n, auditID)
		if err != nil {
			return total, err
		}
		total += deleted
		if deleted == 0 {
			return total, nil
		}
	}
	return total, nil
}
