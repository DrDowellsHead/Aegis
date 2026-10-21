-- Step 5.3: durable incident lifecycle, independent from delivery/notifications.
CREATE TABLE aegis_incidents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES aegis_agents(agent_id),
    rule_id UUID REFERENCES aegis_threshold_rules(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN ('threshold','agent_offline')),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 160),
    state TEXT NOT NULL DEFAULT 'open'
        CHECK (state IN ('open','acknowledged','resolved')),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by TEXT,
    resolved_at TIMESTAMPTZ,
    resolution TEXT,
    CHECK (
        (acknowledged_at IS NULL AND acknowledged_by IS NULL)
        OR (acknowledged_at IS NOT NULL AND length(acknowledged_by) BETWEEN 1 AND 80)
    ),
    CHECK (
        (state='open' AND acknowledged_at IS NULL AND resolved_at IS NULL)
        OR (state='acknowledged' AND acknowledged_at IS NOT NULL AND resolved_at IS NULL)
        OR (state='resolved' AND resolved_at IS NOT NULL AND length(resolution) BETWEEN 1 AND 80)
    ),
    CHECK (kind <> 'agent_offline' OR rule_id IS NULL),
    CHECK (acknowledged_at IS NULL OR acknowledged_at >= opened_at),
    CHECK (resolved_at IS NULL OR resolved_at >= opened_at)
);
-- At most ONE active incident for each rule or agent connectivity problem.
CREATE UNIQUE INDEX aegis_incident_active_rule_idx
    ON aegis_incidents(rule_id)
    WHERE kind='threshold' AND state <> 'resolved';
CREATE UNIQUE INDEX aegis_incident_active_offline_idx
    ON aegis_incidents(agent_id)
    WHERE kind='agent_offline' AND state <> 'resolved';
CREATE INDEX aegis_incidents_feed_idx ON aegis_incidents(opened_at DESC,id DESC);
CREATE INDEX aegis_incidents_agent_feed_idx ON aegis_incidents(agent_id,opened_at DESC,id DESC);

CREATE TABLE aegis_incident_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    incident_id UUID NOT NULL REFERENCES aegis_incidents(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN ('opened','acknowledged','resolved')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 80),
    occurred_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL DEFAULT '' CHECK (length(reason) <= 80)
);
CREATE INDEX aegis_incident_events_incident_idx
    ON aegis_incident_events(incident_id,id);

-- Migrate any conditions already firing after step 5.1. Their original
-- transition time is preserved where available; no duplicate is possible.
WITH prior AS (
    INSERT INTO aegis_incidents(agent_id,rule_id,kind,title,state,opened_at)
    SELECT r.agent_id,r.id,'threshold',left(r.name,160),'open',
           LEAST(COALESCE(s.changed_at,clock_timestamp()),clock_timestamp())
    FROM aegis_threshold_rules r
    JOIN aegis_rule_states s ON s.rule_id=r.id
    WHERE s.firing=TRUE
    RETURNING id,opened_at
)
INSERT INTO aegis_incident_events(incident_id,action,actor,occurred_at,reason)
SELECT id,'opened','system',opened_at,'migration' FROM prior;
