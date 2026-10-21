-- Keep retention scans incremental and independent of the agent's clock.
-- The existing PRIMARY KEY protects deduplication; this index serves purging.
CREATE INDEX aegis_measurements_retention_idx
    ON aegis_measurements (received_at, agent_id, measurement_id);
