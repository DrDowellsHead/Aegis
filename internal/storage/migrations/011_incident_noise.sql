-- 5.4: a single explicit, durable fleet policy (no background scheduler).
CREATE TABLE aegis_incident_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    cooldown_seconds INTEGER NOT NULL DEFAULT 300
        CHECK (cooldown_seconds BETWEEN 0 AND 86400),
    escalate_after_seconds INTEGER NOT NULL DEFAULT 900
        CHECK (escalate_after_seconds BETWEEN 60 AND 604800),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO aegis_incident_settings(id) VALUES(1);

ALTER TABLE aegis_incidents
    ADD COLUMN severity TEXT NOT NULL DEFAULT 'warning'
        CHECK (severity IN ('warning','critical')),
    ADD COLUMN escalated_at TIMESTAMPTZ,
    ADD COLUMN suppressed_checks BIGINT NOT NULL DEFAULT 0
        CHECK (suppressed_checks >= 0),
    ADD COLUMN last_suppressed_at TIMESTAMPTZ,
    ADD CONSTRAINT aegis_incidents_severity_consistency CHECK (
        (severity = 'warning' AND escalated_at IS NULL) OR
        (severity = 'critical' AND escalated_at IS NOT NULL)
    );
ALTER TABLE aegis_incident_events
    DROP CONSTRAINT IF EXISTS aegis_incident_events_action_check;
ALTER TABLE aegis_incident_events
    ADD CONSTRAINT aegis_incident_events_action_check CHECK
        (action IN ('opened','acknowledged','resolved','escalated'));
-- Index for latest completed episode of each source.
CREATE INDEX aegis_incidents_rule_resolved_idx
    ON aegis_incidents(rule_id, resolved_at DESC, id DESC)
    WHERE kind='threshold' AND state='resolved';
CREATE INDEX aegis_incidents_agent_resolved_idx
    ON aegis_incidents(agent_id, resolved_at DESC, id DESC)
    WHERE kind='agent_offline' AND state='resolved';
