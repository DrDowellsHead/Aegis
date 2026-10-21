package storage

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

// Audit records administrative intent before a mutation and makes batch
// progress atomic with the associated changes. It never stores secrets.
type AuditEntry struct {
	ID            string     `json:"id"`
	Action        string     `json:"action"`
	Actor         string     `json:"actor"`
	Scope         string     `json:"scope"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	AffectedRows  int64      `json:"affected_rows"`
	RetentionDays *int       `json:"retention_days,omitempty"`
}

var validActor = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,63}$`)

func ValidateActor(actor string) error {
	if !validActor.MatchString(actor) {
		return fmt.Errorf("AEGIS_OPERATOR must be 1..64 characters: letters, digits, _ . @ -")
	}
	return nil
}

func (p *Postgres) BeginAudit(ctx context.Context, action, actor, scope string, days *int) (string, error) {
	if p == nil || p.db == nil {
		return "", fmt.Errorf("database unavailable")
	}
	if err := ValidateActor(actor); err != nil {
		return "", err
	}
	if action != "retention" && action != "backfill" {
		return "", fmt.Errorf("invalid audited action")
	}
	if scope != "all" && scope != "unprojected" && !validStoredAgentID(scope) {
		return "", fmt.Errorf("invalid audit scope")
	}
	if action == "backfill" && (scope != "unprojected" || days != nil) {
		return "", fmt.Errorf("invalid backfill audit metadata")
	}
	if action == "retention" && (days == nil || *days < MinRetentionDays || *days > MaxRetentionDays || scope == "unprojected") {
		return "", fmt.Errorf("invalid retention audit metadata")
	}
	var id string
	err := p.db.QueryRowContext(ctx, `
        INSERT INTO aegis_admin_audit(action,actor,scope,retention_days)
        VALUES($1,$2,$3,$4) RETURNING id::text
    `, action, actor, scope, days).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("begin administrative audit: %w", err)
	}
	return id, nil
}

func validStoredAgentID(s string) bool {
	// Agent ID validation is defined by identity, but auditing can validate
	// its own fixed UUID shape without depending on authorization state.
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (p *Postgres) FinishAudit(ctx context.Context, id, status string) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("database unavailable")
	}
	if !validStoredAgentID(id) {
		return fmt.Errorf("invalid audit ID")
	}
	if status != "succeeded" && status != "failed" && status != "interrupted" {
		return fmt.Errorf("invalid completion status")
	}
	res, err := p.db.ExecContext(ctx, `
        UPDATE aegis_admin_audit
        SET status=$2,finished_at=clock_timestamp()
        WHERE id=$1 AND status='running'
    `, id, status)
	if err != nil {
		return fmt.Errorf("finish administrative audit: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("administrative audit already finished or not found")
	}
	return nil
}

// ListAudit returns newest events first. A running event with an old
// timestamp may indicate an interrupted process; do not infer success.
func (p *Postgres) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("audit list limit must be 1..100")
	}
	rows, err := p.db.QueryContext(ctx, `
        SELECT id::text,action,actor,scope,status,started_at,finished_at,
               affected_rows,retention_days
        FROM aegis_admin_audit
        ORDER BY started_at DESC,id DESC LIMIT $1
    `, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer rows.Close()
	list := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var a AuditEntry
		var finished sql.NullTime
		var days sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Action, &a.Actor, &a.Scope, &a.Status,
			&a.StartedAt, &finished, &a.AffectedRows, &days); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		a.StartedAt = a.StartedAt.UTC()
		if finished.Valid {
			t := finished.Time.UTC()
			a.FinishedAt = &t
		}
		if days.Valid {
			d := int(days.Int64)
			a.RetentionDays = &d
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit: %w", err)
	}
	return list, nil
}

// advanceAudit requires a transaction so success cannot be recorded
// without committing the matching mutation.
func advanceAudit(ctx context.Context, tx *sql.Tx, id string, changed int64) error {
	if id == "" {
		return nil
	}
	if !validStoredAgentID(id) || changed < 0 {
		return fmt.Errorf("invalid audit progress")
	}
	res, err := tx.ExecContext(ctx, `
        UPDATE aegis_admin_audit SET affected_rows=affected_rows+$2
        WHERE id=$1 AND status='running'
    `, id, changed)
	if err != nil {
		return fmt.Errorf("update audit progress: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("audit operation not found or finished")
	}
	return nil
}
