package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

var (
	ErrIncidentNotFound   = errors.New("incident not found")
	ErrIncidentTransition = errors.New("invalid incident transition")
	ErrIncidentAgent      = errors.New("registered agent not found")
)

type Incident struct {
	ID               string     `json:"id"`
	AgentID          string     `json:"agent_id"`
	RuleID           *string    `json:"rule_id"`
	Kind             string     `json:"kind"`
	Title            string     `json:"title"`
	State            string     `json:"state"`
	OpenedAt         time.Time  `json:"opened_at"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at"`
	AcknowledgedBy   *string    `json:"acknowledged_by"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	Resolution       *string    `json:"resolution"`
	Severity         string     `json:"severity"`
	EscalatedAt      *time.Time `json:"escalated_at"`
	SuppressedChecks int64      `json:"suppressed_checks"`
	LastSuppressedAt *time.Time `json:"last_suppressed_at"`
}

type IncidentQuery struct {
	AgentID string
	State   string // active, resolved, all
	Limit   int
	Cursor  string
}

type IncidentPage struct {
	Items      []Incident `json:"items"`
	HasMore    bool       `json:"has_more"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type IncidentEvent struct {
	Action string    `json:"action"`
	Actor  string    `json:"actor"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

const incidentFields = `id::text,agent_id::text,rule_id::text,kind,title,state,
opened_at,acknowledged_at,acknowledged_by,resolved_at,resolution,
severity,escalated_at,suppressed_checks,last_suppressed_at`

func readIncident(s interface{ Scan(...any) error }) (Incident, error) {
	var v Incident
	var rule, ackBy, resolution sql.NullString
	var ack, resolved, escalated, suppressed sql.NullTime
	err := s.Scan(&v.ID, &v.AgentID, &rule, &v.Kind, &v.Title, &v.State,
		&v.OpenedAt, &ack, &ackBy, &resolved, &resolution,
		&v.Severity, &escalated, &v.SuppressedChecks, &suppressed)
	if err != nil {
		return Incident{}, err
	}
	v.OpenedAt = v.OpenedAt.UTC()
	if escalated.Valid {
		x := escalated.Time.UTC()
		v.EscalatedAt = &x
	}
	if suppressed.Valid {
		x := suppressed.Time.UTC()
		v.LastSuppressedAt = &x
	}
	if rule.Valid {
		x := rule.String
		v.RuleID = &x
	}
	if ack.Valid {
		x := ack.Time.UTC()
		v.AcknowledgedAt = &x
	}
	if ackBy.Valid {
		x := ackBy.String
		v.AcknowledgedBy = &x
	}
	if resolved.Valid {
		x := resolved.Time.UTC()
		v.ResolvedAt = &x
	}
	if resolution.Valid {
		x := resolution.String
		v.Resolution = &x
	}
	return v, nil
}

func incidentEvent(ctx context.Context, tx *sql.Tx, id, action, actor, reason string, at time.Time) error {
	// Always persist the incident transition and notification outbox in the
	// SAME transaction. Network I/O is exclusively performed by the worker.
	var eventID int64
	err := tx.QueryRowContext(ctx, `INSERT INTO aegis_incident_events
		(incident_id,action,actor,reason,occurred_at) VALUES($1,$2,$3,$4,$5)
		RETURNING id`, id, action, actor, reason, at).Scan(&eventID)
	if err != nil {return err}
	if action!="opened" && action!="escalated" && action!="resolved" {
		return nil // acknowledgments remain audit-only; no outbound noise.
	}
	_,err=tx.ExecContext(ctx,`INSERT INTO aegis_notification_jobs
		(event_id,channel,target)
		SELECT $1,channel,target FROM aegis_notification_routes
		WHERE enabled=TRUE
		ON CONFLICT (event_id,channel) DO NOTHING`,eventID)
	return err
}

// ensureIncident is called INSIDE a rule or agent-row locking transaction.
// Partial unique indexes are an additional defense against duplicate active
// incidents across concurrent requests or process restarts.
func ensureIncident(ctx context.Context, tx *sql.Tx, agentID, ruleID, kind, title string, at time.Time) error {
	policy, err := incidentSettingsTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("load incident policy: %w", err)
	}
	var id, state string
	var opened time.Time
	var escalated sql.NullTime
	var source, sourceID string
	if kind == "threshold" {
		source, sourceID = "rule_id", ruleID
	} else {
		source, sourceID = "agent_id", agentID
	}
	// The selected SQL identifier is from a fixed internal enum, not input.
	err = tx.QueryRowContext(ctx, `SELECT id::text,state,opened_at,escalated_at
		FROM aegis_incidents WHERE `+source+`=$1 AND kind=$2
		AND state<>'resolved' FOR UPDATE`, sourceID, kind).
		Scan(&id, &state, &opened, &escalated)
	switch {
	case err == nil:
		// Only unresolved AND unacknowledged problems escalate. Once per
		// episode, with an event written in the same transaction.
		if state == "open" && !escalated.Valid &&
			!at.Before(opened.Add(time.Duration(policy.EscalateAfterSeconds)*time.Second)) {
			_, err = tx.ExecContext(ctx, `UPDATE aegis_incidents SET severity='critical',
				escalated_at=$2 WHERE id=$1`, id, at)
			if err != nil {
				return fmt.Errorf("escalate incident: %w", err)
			}
			if err = incidentEvent(ctx, tx, id, "escalated", "system", "unacknowledged_timeout", at); err != nil {
				return fmt.Errorf("record escalation: %w", err)
			}
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("find active incident: %w", err)
	}
	// No active episode. Check latest resolved incident for this source.
	// Cooldown suppresses repeat episodes without erasing their history.
	if policy.CooldownSeconds > 0 {
		var previousID string
		var lastResolved time.Time
		err = tx.QueryRowContext(ctx, `SELECT id::text,resolved_at
			FROM aegis_incidents WHERE `+source+`=$1 AND kind=$2
			AND state='resolved' ORDER BY resolved_at DESC,id DESC LIMIT 1`,
			sourceID, kind).Scan(&previousID, &lastResolved)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read incident cooldown: %w", err)
		}
		if err == nil && at.Before(lastResolved.Add(time.Duration(policy.CooldownSeconds)*time.Second)) {
			_, err = tx.ExecContext(ctx, `UPDATE aegis_incidents SET
				suppressed_checks=suppressed_checks+1,last_suppressed_at=$2 WHERE id=$1`,
				previousID, at)
			if err != nil {
				return fmt.Errorf("record suppressed incident check: %w", err)
			}
			return nil
		}
	}
	if kind == "threshold" {
		err = tx.QueryRowContext(ctx, `INSERT INTO aegis_incidents
			(agent_id,rule_id,kind,title,opened_at) VALUES($1,$2,'threshold',$3,$4)
			ON CONFLICT (rule_id) WHERE kind='threshold' AND state<>'resolved'
			DO NOTHING RETURNING id::text`, agentID, ruleID, title, at).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `INSERT INTO aegis_incidents
			(agent_id,kind,title,opened_at) VALUES($1,'agent_offline',$2,$3)
			ON CONFLICT (agent_id) WHERE kind='agent_offline' AND state<>'resolved'
			DO NOTHING RETURNING id::text`, agentID, title, at).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open incident: %w", err)
	}
	if err = incidentEvent(ctx, tx, id, "opened", "system", "condition_detected", at); err != nil {
		return fmt.Errorf("record incident opened event: %w", err)
	}
	return nil
}

func resolveIncident(ctx context.Context, tx *sql.Tx, agentID, ruleID, kind, reason string, at time.Time) error {
	var rows *sql.Rows
	var err error
	if kind == "threshold" {
		rows, err = tx.QueryContext(ctx, `UPDATE aegis_incidents SET state='resolved',
			resolved_at=$2,resolution=$3 WHERE rule_id=$1 AND
			kind='threshold' AND state<>'resolved' RETURNING id::text`, ruleID, at, reason)
	} else {
		rows, err = tx.QueryContext(ctx, `UPDATE aegis_incidents SET state='resolved',
			resolved_at=$2,resolution=$3 WHERE agent_id=$1 AND
			kind='agent_offline' AND state<>'resolved' RETURNING id::text`, agentID, at, reason)
	}
	if err != nil {
		return fmt.Errorf("resolve incident: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = incidentEvent(ctx, tx, id, "resolved", "system", reason, at); err != nil {
			return fmt.Errorf("record incident resolved event: %w", err)
		}
	}
	return nil
}

// EvaluateAgentLiveness is deliberately explicit in step 5.3.
// An UNKNOWN never-contacted agent does not generate an offline incident.
// This row lock serializes evaluation with authenticated heartbeat updates.
func (p *Postgres) EvaluateAgentLiveness(ctx context.Context, agentID string, offlineAfter time.Duration) (string, error) {
	if !identity.ValidID(agentID) || offlineAfter < 10*time.Second ||
		offlineAfter > 24*time.Hour || offlineAfter%time.Second != 0 {
		return "", fmt.Errorf("invalid liveness evaluation arguments")
	}
	if p == nil || p.db == nil {
		return "", fmt.Errorf("database unavailable")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var contact sql.NullTime
	var registered bool
	err = tx.QueryRowContext(ctx, `SELECT registered,last_contact_at
		FROM aegis_agents WHERE agent_id=$1 FOR UPDATE`, agentID).
		Scan(&registered, &contact)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrIncidentAgent
	}
	if err != nil {
		return "", err
	}
	if !registered {
		return "", ErrIncidentAgent
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return "", err
	}
	now = now.UTC()
	state := "unknown"
	switch {
	case !contact.Valid:
		// No authenticated receipt is not evidence of losing a connection.
	case !contact.Time.Before(now.Add(-offlineAfter)):
		state = "online"
		if err = resolveIncident(ctx, tx, agentID, "", "agent_offline", "contact_restored", now); err != nil {
			return "", err
		}
	default:
		state = "offline"
		if err = ensureIncident(ctx, tx, agentID, "", "agent_offline", "Agent connection lost", now); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", fmt.Errorf("commit liveness evaluation: %w", err)
	}
	return state, nil
}

// Validated keyset cursor: fixed timestamp|UUID, base64url-encoded.
func parseIncidentCursor(cursor string) (any, any, error) {
	if cursor == "" {
		return nil, nil, nil
	}
	if len(cursor) > 160 {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 || !identity.ValidID(parts[1]) {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid cursor")
	}
	return at.UTC(), parts[1], nil
}
func makeIncidentCursor(v Incident) string {
	return base64.RawURLEncoding.EncodeToString([]byte(v.OpenedAt.Format(time.RFC3339Nano) + "|" + v.ID))
}

// ValidateIncidentQuery is shared by the HTTP gateway and persistence layer,
// so malformed pagination cursors are rejected before any database query.
func ValidateIncidentQuery(q IncidentQuery) error {
	if q.Limit < 1 || q.Limit > 100 || (q.State != "active" && q.State != "resolved" && q.State != "all") ||
		(q.AgentID != "" && !identity.ValidID(q.AgentID)) {
		return fmt.Errorf("invalid incident query")
	}
	_, _, err := parseIncidentCursor(q.Cursor)
	return err
}

func (p *Postgres) ListIncidents(ctx context.Context, q IncidentQuery) (IncidentPage, error) {
	if err := ValidateIncidentQuery(q); err != nil {
		return IncidentPage{}, err
	}
	at, id, err := parseIncidentCursor(q.Cursor)
	if err != nil {
		return IncidentPage{}, err
	}
	if p == nil || p.db == nil {
		return IncidentPage{}, fmt.Errorf("database unavailable")
	}
	var agent any
	if q.AgentID != "" {
		agent = q.AgentID
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+incidentFields+` FROM aegis_incidents
		WHERE ($1::uuid IS NULL OR agent_id=$1::uuid)
		  AND ($2::text='all' OR
		       ($2::text='active' AND state<>'resolved') OR state=$2::text)
		  AND ($3::timestamptz IS NULL OR (opened_at,id)<($3::timestamptz,$4::uuid))
		ORDER BY opened_at DESC,id DESC LIMIT $5
	`, agent, q.State, at, id, q.Limit+1)
	if err != nil {
		return IncidentPage{}, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	page := IncidentPage{Items: make([]Incident, 0, q.Limit)}
	for rows.Next() {
		v, err := readIncident(rows)
		if err != nil {
			return IncidentPage{}, err
		}
		if len(page.Items) == q.Limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, v)
	}
	if err = rows.Err(); err != nil {
		return IncidentPage{}, err
	}
	if page.HasMore {
		page.NextCursor = makeIncidentCursor(page.Items[len(page.Items)-1])
	}
	return page, nil
}

// ChangeIncident is a compare-and-transition operation. All transitions and
// events commit together. A repeated or stale action returns a conflict.
func (p *Postgres) ChangeIncident(ctx context.Context, id, action, actor string) (Incident, error) {
	if !identity.ValidID(id) || (action != "acknowledge" && action != "resolve") ||
		len(actor) == 0 || len(actor) > 80 || strings.TrimSpace(actor) != actor {
		return Incident{}, fmt.Errorf("invalid incident action")
	}
	if p == nil || p.db == nil {
		return Incident{}, fmt.Errorf("database unavailable")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback()
	var oldState string
	err = tx.QueryRowContext(ctx, "SELECT state FROM aegis_incidents WHERE id=$1 FOR UPDATE", id).Scan(&oldState)
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrIncidentNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	if oldState == "resolved" || (action == "acknowledge" && oldState != "open") {
		return Incident{}, ErrIncidentTransition
	}
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return Incident{}, err
	}
	at = at.UTC()
	if action == "acknowledge" {
		_, err = tx.ExecContext(ctx, `UPDATE aegis_incidents SET state='acknowledged',
			acknowledged_at=$2,acknowledged_by=$3 WHERE id=$1`, id, at, actor)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE aegis_incidents SET state='resolved',
			resolved_at=$2,resolution='operator_resolved' WHERE id=$1`, id, at)
	}
	if err != nil {
		return Incident{}, err
	}
	event := "acknowledged"
	if action == "resolve" {
		event = "resolved"
	}
	reason := ""
	if action == "resolve" {
		reason = "operator_resolved"
	}
	if err = incidentEvent(ctx, tx, id, event, actor, reason, at); err != nil {
		return Incident{}, err
	}
	record, err := readIncident(tx.QueryRowContext(ctx, "SELECT "+incidentFields+" FROM aegis_incidents WHERE id=$1", id))
	if err != nil {
		return Incident{}, err
	}
	if err = tx.Commit(); err != nil {
		return Incident{}, err
	}
	return record, nil
}

func (p *Postgres) ListIncidentEvents(ctx context.Context, id string) ([]IncidentEvent, error) {
	if !identity.ValidID(id) {
		return nil, fmt.Errorf("invalid incident ID")
	}
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	rows, err := p.db.QueryContext(ctx, `SELECT action,actor,occurred_at,reason
		FROM aegis_incident_events WHERE incident_id=$1 ORDER BY id ASC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]IncidentEvent, 0)
	for rows.Next() {
		var e IncidentEvent
		if err = rows.Scan(&e.Action, &e.Actor, &e.At, &e.Reason); err != nil {
			return nil, err
		}
		e.At = e.At.UTC()
		events = append(events, e)
	}
	return events, rows.Err()
}
