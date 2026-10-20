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
	res, err := p.db.ExecContext(ctx, `
		INSERT INTO aegis_measurements
			(agent_id, measurement_id, schema_version, started_at, payload, payload_sha256)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (agent_id,measurement_id) DO NOTHING
	`, agentID, measurementID, int32(snapshot.SchemaVersion), snapshot.StartedAt.AsTime(), wire, digest[:])
	if err != nil {
		return false, fmt.Errorf("persist measurement: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check insert result: %w", err)
	}
	if affected == 1 {
		return false, nil
	}
	if affected != 0 {
		return false, fmt.Errorf("unexpected inserted row count: %d", affected)
	}
	var previous []byte
	if err := p.db.QueryRowContext(ctx,
		"SELECT payload_sha256 FROM aegis_measurements WHERE agent_id=$1 AND measurement_id=$2",
		agentID, measurementID).Scan(&previous); err != nil {
		return false, fmt.Errorf("read existing measurement: %w", err)
	}
	if subtle.ConstantTimeCompare(previous, digest[:]) != 1 {
		return false, ErrPayloadMismatch
	}
	return true, nil
}
