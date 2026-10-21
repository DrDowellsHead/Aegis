package storage

import (
	"context"
	"fmt"
)

// EmptyForRestore refuses a restore into a database with application objects.
// The caller must connect to a separately created, empty PostgreSQL database.
// In particular this prevents accidentally restoring on top of live Aegis data.
func (p *Postgres) EmptyForRestore(ctx context.Context) (bool, error) {
	if p == nil || p.db == nil {
		return false, fmt.Errorf("database unavailable")
	}
	var empty bool
	err := p.db.QueryRowContext(ctx, `
		SELECT NOT EXISTS (
			SELECT 1 FROM pg_class AS c
			JOIN pg_namespace AS n ON n.oid = c.relnamespace
			WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
			  AND n.nspname NOT LIKE 'pg_toast%'
		) AND NOT EXISTS (
			SELECT 1 FROM pg_proc AS p
			JOIN pg_namespace AS n ON n.oid = p.pronamespace
			WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
			  AND n.nspname NOT LIKE 'pg_toast%'
		) AND NOT EXISTS (
			SELECT 1 FROM pg_extension WHERE extname <> 'plpgsql'
		)`).Scan(&empty)
	if err != nil {
		return false, fmt.Errorf("inspect restore target: %w", err)
	}
	return empty, nil
}
