CREATE TABLE aegis_measurements (
    agent_id UUID NOT NULL,
    measurement_id TEXT NOT NULL CHECK (measurement_id ~ '^[0-9a-f]{32}$'),
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    started_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    payload BYTEA NOT NULL,
    payload_sha256 BYTEA NOT NULL CHECK (octet_length(payload_sha256) = 32),
    PRIMARY KEY (agent_id, measurement_id)
);
