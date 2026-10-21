// Package storage owns Aegis's PostgreSQL persistence and schema migrations.
package storage

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
	pb "github.com/DrDowellsHead/Aegis/internal/telemetry/v1"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/protobuf/proto"
)

//go:embed migrations/*.sql
var migrations embed.FS

// ErrPayloadMismatch prevents reusing an ID to overwrite an earlier observation.
var ErrPayloadMismatch = errors.New("measurement ID already belongs to different data")

// Postgres executes each INSERT in PostgreSQL's implicit transaction.
// An acknowledged insert has committed if database durability settings are safe.
type Postgres struct{ db *sql.DB }

func Open(ctx context.Context, dsn string) (*Postgres, error) {
	if dsn == "" {
		return nil, fmt.Errorf("AEGIS_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	var fsync, synchronousCommit string
	if err := db.QueryRowContext(ctx,
		"SELECT current_setting('fsync'), current_setting('synchronous_commit')").
		Scan(&fsync, &synchronousCommit); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("check PostgreSQL durability settings: %w", err)
	}
	if fsync != "on" || synchronousCommit == "off" {
		_ = db.Close()
		return nil, fmt.Errorf("PostgreSQL requires fsync=on and synchronous_commit != off for durable ACK")
	}
	return &Postgres{db: db}, nil
}

// Stats exposes database/sql pool diagnostics without running an SQL query.
func (p *Postgres) Stats() sql.DBStats {
	if p == nil || p.db == nil { return sql.DBStats{} }
	return p.db.Stats()
}

func (p *Postgres) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

// Migrate serializes concurrent schema migrations through an advisory lock.
// The migration ledger and schema change commit in the same transaction.
func (p *Postgres) Migrate(ctx context.Context) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(176341, 4)"); err != nil {
		return fmt.Errorf("lock schema migrations: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS aegis_schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	for _, name := range files {
		version := strings.TrimPrefix(name, "migrations/")
		var exists bool
		if err := tx.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM aegis_schema_migrations WHERE version=$1)", version).
			Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO aegis_schema_migrations(version) VALUES($1)", version); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// Save returns already=true only for the exact same (agent, ID, payload).
// The first INSERT is atomic and its success is not reported before commit.
func (p *Postgres) Save(ctx context.Context, agentID, measurementID string, snapshot *pb.Snapshot) (bool, error) {
	if !identity.ValidID(agentID) || len(measurementID) != 32 || snapshot == nil {
		return false, fmt.Errorf("invalid measurement identity or snapshot")
	}
	for _, c := range measurementID {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false, fmt.Errorf("invalid measurement ID")
		}
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(snapshot)
	if err != nil {
		return false, fmt.Errorf("encode measurement: %w", err)
	}
	digest := sha256.Sum256(wire)
	cpu, memory, disk, path, err := project(snapshot)
	if err != nil {
		return false, fmt.Errorf("project measurement: %w", err)
	}

	// The telemetry row and the agent's last authenticated contact MUST
	// commit together. A lost ACK can be retried with the same ID.
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin telemetry transaction: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO aegis_measurements
			(agent_id, measurement_id, schema_version, started_at, payload, payload_sha256,
			 cpu_used_percent, memory_used_percent, disk_used_percent, disk_path, projected)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,TRUE)
		ON CONFLICT (agent_id,measurement_id) DO NOTHING
	`, agentID, measurementID, int32(snapshot.SchemaVersion), snapshot.StartedAt.AsTime(),
		wire, digest[:], cpu, memory, disk, path)
	if err != nil {
		return false, fmt.Errorf("persist measurement: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check insert result: %w", err)
	}
	if affected != 0 && affected != 1 {
		return false, fmt.Errorf("unexpected inserted row count: %d", affected)
	}
	already := affected == 0
	if already {
		var previous []byte
		if err := tx.QueryRowContext(ctx,
			"SELECT payload_sha256 FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2",
			agentID, measurementID).Scan(&previous); err != nil {
			return false, fmt.Errorf("read existing measurement: %w", err)
		}
		if subtle.ConstantTimeCompare(previous, digest[:]) != 1 {
			return false, ErrPayloadMismatch
		}
	}
	// Even a valid deduplicated request proves recent authenticated contact.
	// The agent's sample timestamp is a separate, possibly inaccurate clock.
	// Historical replays must NOT overwrite OS/architecture from newer samples.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO aegis_agents
			(agent_id,registered,last_contact_at,last_telemetry_at,last_sample_at,last_os,last_arch)
		VALUES($1,FALSE,clock_timestamp(),clock_timestamp(),$2,$3,$4)
		ON CONFLICT (agent_id) DO UPDATE SET
			last_contact_at=GREATEST(COALESCE(aegis_agents.last_contact_at,
				'-infinity'::timestamptz),clock_timestamp()),
			last_telemetry_at=GREATEST(COALESCE(aegis_agents.last_telemetry_at,
				'-infinity'::timestamptz),clock_timestamp()),
			last_os=CASE WHEN EXCLUDED.last_sample_at >=
				COALESCE(aegis_agents.last_sample_at,'-infinity'::timestamptz)
				THEN EXCLUDED.last_os ELSE aegis_agents.last_os END,
			last_arch=CASE WHEN EXCLUDED.last_sample_at >=
				COALESCE(aegis_agents.last_sample_at,'-infinity'::timestamptz)
				THEN EXCLUDED.last_arch ELSE aegis_agents.last_arch END,
			last_sample_at=GREATEST(aegis_agents.last_sample_at,EXCLUDED.last_sample_at)
	`, agentID, snapshot.StartedAt.AsTime(), snapshot.Os, snapshot.Arch); err != nil {
		return false, fmt.Errorf("record authenticated agent contact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit telemetry and agent contact: %w", err)
	}
	return already, nil
}
