package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/alerts"
	"github.com/DrDowellsHead/Aegis/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrRuleAgent = errors.New("registered agent not found")
var ErrRuleExists = errors.New("rule with this name already exists")
var ErrRuleLimit = errors.New("maximum of 50 rules per agent")

type RuleInput struct {
	AgentID string `json:"agent_id"`
	alerts.Rule
}

type RuleRecord struct {
	RuleInput
	ID           string     `json:"id"`
	Firing       bool       `json:"firing"`
	LastStatus   string     `json:"last_status"`
	LastAverage  *float64   `json:"last_average"`
	SampleCount  int64      `json:"sample_count"`
	LastSampleAt *time.Time `json:"last_sample_at"`
	EvaluatedAt  *time.Time `json:"evaluated_at"`
	ChangedAt    *time.Time `json:"changed_at"`
}

func validateRuleInput(input RuleInput) error {
	if !identity.ValidID(input.AgentID) {
		return fmt.Errorf("invalid agent ID")
	}
	return alerts.Validate(input.Rule)
}

const ruleColumns = `r.id::text,r.agent_id::text,r.name,r.metric,r.direction,
r.fire_at,r.recover_at,r.window_seconds,r.min_samples,r.max_age_seconds,
r.disk_path,s.firing,s.last_status,s.last_average,s.sample_count,
s.last_sample_at,s.evaluated_at,s.changed_at`

func readRule(rows interface{ Scan(...any) error }) (RuleRecord, error) {
	var r RuleRecord
	var value sql.NullFloat64
	var last, evaluated, changed sql.NullTime
	err := rows.Scan(
		&r.ID, &r.AgentID, &r.Name, &r.Metric, &r.Direction,
		&r.FireAt, &r.RecoverAt, &r.WindowSeconds, &r.MinSamples,
		&r.MaxAgeSeconds, &r.DiskPath, &r.Firing, &r.LastStatus,
		&value, &r.SampleCount, &last, &evaluated, &changed,
	)
	if err != nil {
		return RuleRecord{}, err
	}
	r.LastAverage = floatPointer(value)
	if last.Valid {
		v := last.Time.UTC()
		r.LastSampleAt = &v
	}
	if evaluated.Valid {
		v := evaluated.Time.UTC()
		r.EvaluatedAt = &v
	}
	if changed.Valid {
		v := changed.Time.UTC()
		r.ChangedAt = &v
	}
	return r, nil
}

// CreateRule serializes rule creation per agent; only evaluations can open
// incidents. Duplicate names are rejected.
func (p *Postgres) CreateRule(ctx context.Context, input RuleInput) (RuleRecord, error) {
	if err := validateRuleInput(input); err != nil {
		return RuleRecord{}, err
	}
	if p == nil || p.db == nil {
		return RuleRecord{}, fmt.Errorf("database unavailable")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return RuleRecord{}, fmt.Errorf("begin rule create: %w", err)
	}
	defer tx.Rollback()
	var registered bool
	if err := tx.QueryRowContext(ctx,
		"SELECT registered FROM aegis_agents WHERE agent_id=$1 FOR UPDATE", input.AgentID).Scan(&registered); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RuleRecord{}, ErrRuleAgent
		}
		return RuleRecord{}, fmt.Errorf("validate rule agent: %w", err)
	}
	if !registered {
		return RuleRecord{}, ErrRuleAgent
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM aegis_threshold_rules WHERE agent_id=$1", input.AgentID).Scan(&count); err != nil {
		return RuleRecord{}, fmt.Errorf("count threshold rules: %w", err)
	}
	if count >= 50 {
		return RuleRecord{}, ErrRuleLimit
	}
	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO aegis_threshold_rules
			(agent_id,name,metric,direction,fire_at,recover_at,
			 window_seconds,min_samples,max_age_seconds,disk_path)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text
	`, input.AgentID, input.Name, input.Metric, input.Direction,
		input.FireAt, input.RecoverAt, input.WindowSeconds,
		input.MinSamples, input.MaxAgeSeconds, input.DiskPath).Scan(&id)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return RuleRecord{}, ErrRuleExists
		}
		return RuleRecord{}, fmt.Errorf("create threshold rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO aegis_rule_states (rule_id) VALUES($1)", id); err != nil {
		return RuleRecord{}, fmt.Errorf("initialize threshold state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RuleRecord{}, fmt.Errorf("commit threshold rule: %w", err)
	}
	return RuleRecord{RuleInput: input, ID: id, LastStatus: "not_evaluated"}, nil
}

func (p *Postgres) ListRules(ctx context.Context, agentID string) ([]RuleRecord, error) {
	if !identity.ValidID(agentID) {
		return nil, fmt.Errorf("invalid agent ID")
	}
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	rows, err := p.db.QueryContext(ctx, "SELECT "+ruleColumns+
		" FROM aegis_threshold_rules r JOIN aegis_rule_states s ON s.rule_id=r.id WHERE r.agent_id=$1 ORDER BY r.name,r.id", agentID)
	if err != nil {
		return nil, fmt.Errorf("list threshold rules: %w", err)
	}
	defer rows.Close()
	out := make([]RuleRecord, 0)
	for rows.Next() {
		r, err := readRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan threshold rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate threshold rules: %w", err)
	}
	return out, nil
}

func (p *Postgres) DeleteRule(ctx context.Context, ruleID string) (bool, error) {
	if !identity.ValidID(ruleID) {
		return false, fmt.Errorf("invalid rule ID")
	}
	if p == nil || p.db == nil {
		return false, fmt.Errorf("database unavailable")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var agentID string
	err = tx.QueryRowContext(ctx, `SELECT agent_id::text FROM aegis_threshold_rules
		WHERE id=$1 FOR UPDATE`, ruleID).Scan(&agentID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return false, err
	}
	if err = resolveIncident(ctx, tx, agentID, ruleID, "threshold", "rule_deleted", at.UTC()); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM aegis_threshold_rules WHERE id=$1", ruleID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func aggregateRule(ctx context.Context, tx *sql.Tx, r RuleRecord, at time.Time) (alerts.Observation, error) {
	var column string
	switch r.Metric {
	case "cpu":
		column = "cpu_used_percent"
	case "memory":
		column = "memory_used_percent"
	case "disk":
		column = "disk_used_percent"
	default:
		return alerts.Observation{}, fmt.Errorf("unknown persisted metric")
	}
	// SQL identifiers are selected from a compile-time whitelist.
	query := fmt.Sprintf(`SELECT COUNT(*), AVG(%s), MAX(started_at)
		FROM aegis_measurements WHERE agent_id=$1 AND projected=TRUE
		AND started_at > $2 AND started_at <= $3 AND %s IS NOT NULL
		AND ($4::text='' OR disk_path=$4::text)`, column, column)
	var obs alerts.Observation
	var average sql.NullFloat64
	var latest sql.NullTime
	err := tx.QueryRowContext(ctx, query, r.AgentID,
		at.Add(-time.Duration(r.WindowSeconds)*time.Second), at, r.DiskPath).
		Scan(&obs.Samples, &average, &latest)
	if err != nil {
		return alerts.Observation{}, fmt.Errorf("aggregate rule metric: %w", err)
	}
	obs.Average = floatPointer(average)
	if latest.Valid {
		last := latest.Time.UTC()
		obs.Latest = &last
	}
	return obs, nil
}

// EvaluateRules stores the firing latch and incident lifecycle atomically.
// Rules are locked to avoid duplicate transitions under concurrent requests.
// The time window uses measured (started_at) UTC, never received_at.
func (p *Postgres) EvaluateRules(ctx context.Context, agentID string) ([]RuleRecord, error) {
	if !identity.ValidID(agentID) {
		return nil, fmt.Errorf("invalid agent ID")
	}
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin rule evaluation: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT "+ruleColumns+
		" FROM aegis_threshold_rules r JOIN aegis_rule_states s ON s.rule_id=r.id WHERE r.agent_id=$1 ORDER BY r.id FOR UPDATE OF r,s", agentID)
	if err != nil {
		return nil, fmt.Errorf("lock threshold rules: %w", err)
	}
	records := make([]RuleRecord, 0)
	for rows.Next() {
		r, err := readRule(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan evaluation rule: %w", err)
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("read evaluation rules: %w", err)
	}
	// Read the PostgreSQL clock AFTER acquiring row locks. Otherwise a
	// queued concurrent evaluator could use an older time than the
	// evaluator that just committed an incident, reversing its timestamps.
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return nil, fmt.Errorf("read rule evaluation clock: %w", err)
	}
	at = at.UTC().Truncate(time.Microsecond)

	for i := range records {
		r := &records[i]
		if err := alerts.Validate(r.Rule); err != nil {
			return nil, fmt.Errorf("persisted rule invalid: %w", err)
		}
		obs, err := aggregateRule(ctx, tx, *r, at)
		if err != nil {
			return nil, err
		}
		out := alerts.Evaluate(r.Rule, r.Firing, obs, at)
		_, err = tx.ExecContext(ctx, `
			UPDATE aegis_rule_states SET firing=$2,last_status=$3,
			last_average=$4,sample_count=$5,last_sample_at=$6,evaluated_at=$7,
			changed_at=CASE WHEN firing<>$2 THEN $7 ELSE changed_at END
			WHERE rule_id=$1
		`, r.ID, out.Firing, out.Status, obs.Average, obs.Samples, obs.Latest, at)
		if err != nil {
			return nil, fmt.Errorf("store threshold evaluation: %w", err)
		}
		// The incident and its state transition are committed in the very
		// same transaction as the rule latch; no half-written incidents.
		if out.Firing {
			err = ensureIncident(ctx, tx, r.AgentID, r.ID, "threshold", r.Name, at)
		} else {
			err = resolveIncident(ctx, tx, r.AgentID, r.ID, "threshold", "condition_recovered", at)
		}
		if err != nil {
			return nil, fmt.Errorf("synchronize rule incident: %w", err)
		}
		if out.Firing != r.Firing {
			changed := at
			r.ChangedAt = &changed
		}
		r.Firing, r.LastStatus, r.LastAverage = out.Firing, out.Status, obs.Average
		r.SampleCount, r.LastSampleAt, r.EvaluatedAt = obs.Samples, obs.Latest, &at
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit threshold evaluation: %w", err)
	}
	return records, nil
}
