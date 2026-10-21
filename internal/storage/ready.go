package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// Ready is intentionally separate from Migrate: the server will not
// silently mutate production schemas when starting.
func (p *Postgres) Ready(ctx context.Context) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("database unavailable")
	}
	var measurements, ledger sql.NullString
	if err := p.db.QueryRowContext(ctx,
		"SELECT to_regclass('aegis_measurements')::text, to_regclass('aegis_schema_migrations')::text").
		Scan(&measurements, &ledger); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if !measurements.Valid || !ledger.Valid {
		return fmt.Errorf("schema has not been migrated")
	}
	var count int
	if err := p.db.QueryRowContext(ctx,
		"SELECT count(*) FROM aegis_schema_migrations WHERE version IN ('001_measurements.sql','002_started_at_index.sql','003_history_cursor_index.sql','004_chart_projections.sql','005_agent_registry.sql','006_retention_received_at.sql','007_admin_audit.sql','008_threshold_rules.sql','009_heartbeat.sql','010_incidents.sql','011_incident_noise.sql','012_notification_outbox.sql','013_activity_journal.sql')").
		Scan(&count); err != nil {
		return fmt.Errorf("check migration ledger: %w", err)
	}
	if count != 13 {
		return fmt.Errorf("database schema is not up to date")
	}
	return nil
}
