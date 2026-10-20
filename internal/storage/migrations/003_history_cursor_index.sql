-- The preceding index only sorted on started_at. Include the tie-breaker
-- needed for deterministic keyset pagination, without retaining duplicates.
DROP INDEX aegis_measurements_started_at_idx;
CREATE INDEX aegis_measurements_history_idx
    ON aegis_measurements (agent_id, started_at DESC, measurement_id DESC);
