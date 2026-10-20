CREATE INDEX aegis_measurements_started_at_idx
    ON aegis_measurements (agent_id, started_at DESC);
