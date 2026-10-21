package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// IncidentSettings is one fleet-wide policy for the manually evaluated
// detector. Zero cooldown disables suppression for every incident source.
type IncidentSettings struct {
	CooldownSeconds      int       `json:"cooldown_seconds"`
	EscalateAfterSeconds int       `json:"escalate_after_seconds"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func validateIncidentSettings(v IncidentSettings) error {
	if v.CooldownSeconds < 0 || v.CooldownSeconds > 86400 ||
		v.EscalateAfterSeconds < 60 || v.EscalateAfterSeconds > 604800 {
		return fmt.Errorf("incident policy out of bounds")
	}
	return nil
}
func readIncidentSettings(row interface{ Scan(...any) error }) (IncidentSettings, error) {
	var result IncidentSettings
	err := row.Scan(&result.CooldownSeconds, &result.EscalateAfterSeconds, &result.UpdatedAt)
	result.UpdatedAt = result.UpdatedAt.UTC()
	return result, err
}
func incidentSettingsTx(ctx context.Context, tx *sql.Tx) (IncidentSettings, error) {
	return readIncidentSettings(tx.QueryRowContext(ctx, `SELECT cooldown_seconds,
		escalate_after_seconds,updated_at FROM aegis_incident_settings WHERE id=1 FOR SHARE`))
}
func (p *Postgres) GetIncidentSettings(ctx context.Context) (IncidentSettings, error) {
	if p == nil || p.db == nil {
		return IncidentSettings{}, fmt.Errorf("database unavailable")
	}
	return readIncidentSettings(p.db.QueryRowContext(ctx, `SELECT cooldown_seconds,
		escalate_after_seconds,updated_at FROM aegis_incident_settings WHERE id=1`))
}
func (p *Postgres) UpdateIncidentSettings(ctx context.Context, v IncidentSettings) (IncidentSettings, error) {
	if err := validateIncidentSettings(v); err != nil {
		return IncidentSettings{}, err
	}
	if p == nil || p.db == nil {
		return IncidentSettings{}, fmt.Errorf("database unavailable")
	}
	record, err := readIncidentSettings(p.db.QueryRowContext(ctx, `
		UPDATE aegis_incident_settings
		SET cooldown_seconds=$1,escalate_after_seconds=$2,updated_at=clock_timestamp()
		WHERE id=1 RETURNING cooldown_seconds,escalate_after_seconds,updated_at`,
		v.CooldownSeconds, v.EscalateAfterSeconds))
	if err != nil {
		return IncidentSettings{}, fmt.Errorf("update incident settings: %w", err)
	}
	return record, nil
}
