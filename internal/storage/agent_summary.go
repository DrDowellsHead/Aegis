package storage

import (
	"context"
	"fmt"
	"time"
)

// AgentSummary counts registered agents using one database timestamp, so
// the cards always agree internally. Status is telemetry contact freshness,
// not proof that a computer is switched on.
type AgentSummary struct {
	Total        int64     `json:"total"`
	Online       int64     `json:"online"`
	Offline      int64     `json:"offline"`
	Unknown      int64     `json:"unknown"`
	CheckedAt    time.Time `json:"checked_at"`
	OfflineAfter string    `json:"offline_after"`
}

func (p *Postgres) CountAgents(ctx context.Context, threshold time.Duration) (AgentSummary, error) {
	if p == nil || p.db == nil {
		return AgentSummary{}, fmt.Errorf("database unavailable")
	}
	if threshold < 10*time.Second || threshold > 24*time.Hour || threshold%time.Second != 0 {
		return AgentSummary{}, fmt.Errorf("invalid offline threshold")
	}
	var s AgentSummary
	// clock_timestamp() evaluated once by anchor and shared by all buckets.
	err := p.db.QueryRowContext(ctx, `
        WITH anchor AS (SELECT statement_timestamp() AS now)
        SELECT anchor.now,
               count(a.agent_id),
               count(*) FILTER (WHERE a.agent_id IS NOT NULL AND a.last_contact_at >=
                   anchor.now - ($1::bigint * interval '1 second')),
               count(*) FILTER (WHERE a.agent_id IS NOT NULL AND a.last_contact_at <
                   anchor.now - ($1::bigint * interval '1 second')),
               count(*) FILTER (WHERE a.agent_id IS NOT NULL AND a.last_contact_at IS NULL)
        FROM anchor LEFT JOIN aegis_agents a ON a.registered=TRUE
        GROUP BY anchor.now
    `, int64(threshold/time.Second)).Scan(&s.CheckedAt, &s.Total, &s.Online, &s.Offline, &s.Unknown)
	if err != nil {
		return AgentSummary{}, fmt.Errorf("count registered agents: %w", err)
	}
	s.CheckedAt = s.CheckedAt.UTC()
	s.OfflineAfter = threshold.String()
	return s, nil
}
