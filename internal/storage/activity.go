package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// ActivityEntry is a server-authored audit record. No arbitrary browser JSON
// and no credentials, HTTP bodies, URLs or remote IPs are persisted.
type ActivityEntry struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	Category    string         `json:"category"`
	Action      string         `json:"action"`
	Actor       string         `json:"actor"`
	AgentID     *string        `json:"agent_id"`
	IncidentID  *string        `json:"incident_id"`
	ReferenceID string         `json:"reference_id"`
	Summary     string         `json:"summary"`
	Details     map[string]any `json:"details"`
}
type ActivityQuery struct {
	Category   string // all / incident / operator / delivery
	Action     string
	Actor      string
	AgentID    string
	IncidentID string
	Search     string
	From       string // RFC3339, inclusive
	To         string // RFC3339, exclusive
	Limit      int    // 1..100
	Cursor     string // opaque-ish monotonically decreasing numeric activity ID
}
type ActivityPage struct {
	Items      []ActivityEntry `json:"items"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func validActivityAction(a string) bool {
	switch a {
	case "opened", "acknowledged", "resolved", "escalated",
		"rule_created", "rule_deleted", "rules_evaluated",
		"connectivity_evaluated", "policy_updated", "routes_updated",
		"sent", "retry", "dead":
		return true
	}
	return false
}
func validActivityActor(a string) bool {
	if len(a) < 1 || len(a) > 80 {
		return false
	}
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '@') {
			return false
		}
	}
	return true
}
func ValidateActivityQuery(q ActivityQuery) error {
	if q.Limit < 1 || q.Limit > 100 ||
		(q.Category != "" && q.Category != "all" && q.Category != "incident" &&
			q.Category != "operator" && q.Category != "delivery") ||
		(q.Action != "" && !validActivityAction(q.Action)) ||
		(q.Actor != "" && !validActivityActor(q.Actor)) ||
		(q.AgentID != "" && !identity.ValidID(q.AgentID)) ||
		(q.IncidentID != "" && !identity.ValidID(q.IncidentID)) ||
		len(q.Search) > 80 || (q.Search != "" && len(q.Search) < 2) ||
		strings.TrimSpace(q.Search) != q.Search {
		return fmt.Errorf("invalid activity filter")
	}
	if q.Cursor != "" {
		v, e := strconv.ParseInt(q.Cursor, 10, 64)
		if e != nil || v <= 0 || len(q.Cursor) > 19 {
			return fmt.Errorf("invalid activity cursor")
		}
	}
	var from, to time.Time
	for i, s := range []string{q.From, q.To} {
		if s == "" {
			continue
		}
		if len(s) > 40 {
			return fmt.Errorf("invalid audit timestamp")
		}
		t, e := time.Parse(time.RFC3339Nano, s)
		if e != nil {
			return fmt.Errorf("invalid audit timestamp")
		}
		if i == 0 {
			from = t
		} else {
			to = t
		}
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return fmt.Errorf("activity range must be increasing")
	}
	if !from.IsZero() && !to.IsZero() && to.Sub(from) > 366*24*time.Hour {
		return fmt.Errorf("activity range exceeds 366 days")
	}
	return nil
}

func appendOperatorAction(ctx context.Context, tx *sql.Tx, actor, action, agentID, referenceID, summary string, details map[string]any) error {
	if !validActivityActor(actor) || !validActivityAction(action) ||
		len(referenceID) > 64 || len(summary) > 160 {
		return fmt.Errorf("invalid activity metadata")
	}
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil || len(payload) > 1900 {
		return fmt.Errorf("activity metadata too large")
	}
	var agent any
	if agentID != "" {
		if !identity.ValidID(agentID) {
			return fmt.Errorf("invalid activity agent")
		}
		agent = agentID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO aegis_activity_log
		(category,action,actor,agent_id,reference_id,summary,details)
		VALUES('operator',$1,$2,$3,$4,$5,$6::jsonb)`,
		action, actor, agent, referenceID, summary, string(payload))
	return err
}
func escapedLike(raw string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(raw)
}
func (p *Postgres) ListActivity(ctx context.Context, q ActivityQuery) (ActivityPage, error) {
	if err := ValidateActivityQuery(q); err != nil {
		return ActivityPage{}, err
	}
	if p == nil || p.db == nil {
		return ActivityPage{}, fmt.Errorf("database unavailable")
	}
	var cursor int64
	if q.Cursor != "" {
		cursor, _ = strconv.ParseInt(q.Cursor, 10, 64)
	}
	var agent, incident, from, to, category, action, actor, search any
	if q.AgentID != "" {
		agent = q.AgentID
	}
	if q.IncidentID != "" {
		incident = q.IncidentID
	}
	if q.From != "" {
		t, _ := time.Parse(time.RFC3339Nano, q.From)
		from = t.UTC()
	}
	if q.To != "" {
		t, _ := time.Parse(time.RFC3339Nano, q.To)
		to = t.UTC()
	}
	if q.Category != "" && q.Category != "all" {
		category = q.Category
	}
	if q.Action != "" {
		action = q.Action
	}
	if q.Actor != "" {
		actor = q.Actor
	}
	if q.Search != "" {
		search = escapedLike(q.Search)
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id,occurred_at,category,action,actor,
			agent_id::text,incident_id::text,reference_id,summary,details
		FROM aegis_activity_log
		WHERE ($1::text IS NULL OR category=$1)
		  AND ($2::text IS NULL OR action=$2)
		  AND ($3::text IS NULL OR actor=$3)
		  AND ($4::uuid IS NULL OR agent_id=$4::uuid)
		  AND ($5::uuid IS NULL OR incident_id=$5::uuid)
		  AND ($6::timestamptz IS NULL OR occurred_at >= $6::timestamptz)
		  AND ($7::timestamptz IS NULL OR occurred_at < $7::timestamptz)
		  AND ($8::text IS NULL OR summary ILIKE '%' || $8 || '%' ESCAPE '\')
		  AND ($9::bigint=0 OR id<$9)
		ORDER BY id DESC LIMIT $10`,
		category, action, actor, agent, incident, from, to, search, cursor, q.Limit+1)
	if err != nil {
		return ActivityPage{}, fmt.Errorf("query activity: %w", err)
	}
	defer rows.Close()
	page := ActivityPage{Items: make([]ActivityEntry, 0, q.Limit)}
	for rows.Next() {
		var e ActivityEntry
		var agentVal, incVal sql.NullString
		var raw []byte
		if err = rows.Scan(&e.ID, &e.At, &e.Category, &e.Action, &e.Actor,
			&agentVal, &incVal, &e.ReferenceID, &e.Summary, &raw); err != nil {
			return ActivityPage{}, err
		}
		e.At = e.At.UTC()
		if agentVal.Valid {
			v := agentVal.String
			e.AgentID = &v
		}
		if incVal.Valid {
			v := incVal.String
			e.IncidentID = &v
		}
		if err = json.Unmarshal(raw, &e.Details); err != nil {
			return ActivityPage{}, err
		}
		if len(page.Items) == q.Limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, e)
	}
	if err = rows.Err(); err != nil {
		return ActivityPage{}, err
	}
	if page.HasMore {
		page.NextCursor = strconv.FormatInt(page.Items[len(page.Items)-1].ID, 10)
	}
	return page, nil
}
