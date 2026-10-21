-- 014: status of the optional automatic incident evaluator.
-- Advisory session locks are used for election; they are released on crashes.
CREATE TABLE aegis_watch_status (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    state TEXT NOT NULL DEFAULT 'never'
        CHECK (state IN ('never','running','ok','partial_failure','failed')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    progress_at TIMESTAMPTZ,
    agents_checked BIGINT NOT NULL DEFAULT 0 CHECK (agents_checked >= 0),
    rules_checked BIGINT NOT NULL DEFAULT 0 CHECK (rules_checked >= 0),
    errors BIGINT NOT NULL DEFAULT 0 CHECK (errors >= 0),
    interval_seconds INTEGER NOT NULL DEFAULT 30
        CHECK (interval_seconds BETWEEN 10 AND 3600),
    offline_after_seconds INTEGER NOT NULL DEFAULT 60
        CHECK (offline_after_seconds BETWEEN 10 AND 86400)
);
INSERT INTO aegis_watch_status (id) VALUES (1);
