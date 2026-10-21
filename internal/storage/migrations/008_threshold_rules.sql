-- Step 5.1: threshold evaluation only. No incidents or notification side effects.
CREATE TABLE aegis_threshold_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES aegis_agents(agent_id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    metric TEXT NOT NULL CHECK (metric IN ('cpu','memory','disk')),
    direction TEXT NOT NULL CHECK (direction IN ('above','below')),
    fire_at DOUBLE PRECISION NOT NULL CHECK (fire_at >= 0 AND fire_at <= 100),
    recover_at DOUBLE PRECISION NOT NULL CHECK (recover_at >= 0 AND recover_at <= 100),
    window_seconds INTEGER NOT NULL CHECK (window_seconds BETWEEN 60 AND 86400),
    min_samples INTEGER NOT NULL CHECK (min_samples BETWEEN 1 AND 10000),
    max_age_seconds INTEGER NOT NULL CHECK (max_age_seconds BETWEEN 1 AND window_seconds),
    disk_path TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT aegis_rule_direction_hysteresis CHECK (
        (direction='above' AND recover_at < fire_at) OR
        (direction='below' AND recover_at > fire_at)
    ),
    CONSTRAINT aegis_rule_disk_path CHECK (
        (metric='disk' AND length(disk_path) BETWEEN 1 AND 1024) OR
        (metric<>'disk' AND disk_path='')
    ),
    CONSTRAINT aegis_rule_unique_name UNIQUE (agent_id, name)
);
CREATE INDEX aegis_threshold_rules_agent_idx ON aegis_threshold_rules(agent_id, id);

CREATE TABLE aegis_rule_states (
    rule_id UUID PRIMARY KEY REFERENCES aegis_threshold_rules(id) ON DELETE CASCADE,
    firing BOOLEAN NOT NULL DEFAULT FALSE,
    last_status TEXT NOT NULL DEFAULT 'not_evaluated'
        CHECK (last_status IN ('not_evaluated','normal','firing','insufficient_data')),
    last_average DOUBLE PRECISION,
    sample_count BIGINT NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    last_sample_at TIMESTAMPTZ,
    evaluated_at TIMESTAMPTZ,
    changed_at TIMESTAMPTZ
);
