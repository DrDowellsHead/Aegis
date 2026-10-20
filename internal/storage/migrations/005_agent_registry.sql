-- Authorization secrets stay in the private agents.json, never in PostgreSQL.
-- Existing telemetry agents are historical only (registered=false) until
-- an operator starts 'receive' with the authoritative credentials registry.
CREATE TABLE aegis_agents (
    agent_id UUID PRIMARY KEY,
    registered BOOLEAN NOT NULL DEFAULT FALSE,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_contact_at TIMESTAMPTZ,
    last_sample_at TIMESTAMPTZ,
    last_os TEXT,
    last_arch TEXT
);

INSERT INTO aegis_agents (agent_id, registered, last_contact_at, last_sample_at)
SELECT agent_id, FALSE, MAX(received_at), MAX(started_at)
FROM aegis_measurements
GROUP BY agent_id;

CREATE INDEX aegis_agents_registered_order_idx
    ON aegis_agents(agent_id)
    WHERE registered = TRUE;
