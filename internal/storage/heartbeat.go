package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/DrDowellsHead/Aegis/internal/identity"
)

// ErrAgentNotRegistered means the credentials may still be valid in the JSON
// registry, but the corresponding database agent is not currently registered.
var ErrAgentNotRegistered = errors.New("agent is not registered in the database")

// RecordHeartbeat records only a server-side authenticated receipt time.
// A heartbeat does NOT append any measurement, fabricate CPU values, or
// change the latest metric sample. The UPDATE commits before returning.
func (p *Postgres) RecordHeartbeat(ctx context.Context, agentID string) error {
	if !identity.ValidID(agentID) {
		return fmt.Errorf("invalid heartbeat agent ID")
	}
	if p == nil || p.db == nil {
		return fmt.Errorf("heartbeat database unavailable")
	}
	var updated string
	err := p.db.QueryRowContext(ctx, `
		UPDATE aegis_agents
		SET last_heartbeat_at = clock_timestamp(),
		    last_contact_at = GREATEST(
				COALESCE(last_contact_at, '-infinity'::timestamptz),
				clock_timestamp())
		WHERE agent_id=$1 AND registered=TRUE
		RETURNING agent_id::text
	`, agentID).Scan(&updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAgentNotRegistered
	}
	if err != nil {
		return fmt.Errorf("record authenticated heartbeat: %w", err)
	}
	return nil
}
