-- Independent authenticated heartbeat/telemetry receipt timestamps.
-- Prior contact represented telemetry only; preserve it during migration.
ALTER TABLE aegis_agents
    ADD COLUMN last_heartbeat_at TIMESTAMPTZ,
    ADD COLUMN last_telemetry_at TIMESTAMPTZ;
UPDATE aegis_agents SET last_telemetry_at = last_contact_at
WHERE last_contact_at IS NOT NULL;
COMMENT ON COLUMN aegis_agents.last_contact_at IS
    'Last committed authenticated heartbeat or telemetry receipt (server clock).';
COMMENT ON COLUMN aegis_agents.last_heartbeat_at IS
    'Last committed authenticated heartbeat receipt; null for legacy agents.';
COMMENT ON COLUMN aegis_agents.last_telemetry_at IS
    'Last committed authenticated telemetry request (including deduplicated replay).';
