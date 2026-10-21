-- 012: transactional notification outbox, channel routing and delivery audit.
-- Routes contain non-secret target identifiers; never store tokens/passwords.
CREATE TABLE aegis_notification_routes (
    channel TEXT PRIMARY KEY CHECK (channel IN ('email','telegram','webhook')),
    target TEXT NOT NULL CHECK (length(target) BETWEEN 1 AND 2048),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE aegis_notification_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id BIGINT NOT NULL REFERENCES aegis_incident_events(id),
    channel TEXT NOT NULL CHECK (channel IN ('email','telegram','webhook')),
    target TEXT NOT NULL CHECK (length(target) BETWEEN 1 AND 2048),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','leased','retry','sent','dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 8),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lease_token UUID,
    leased_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    sent_at TIMESTAMPTZ,
    last_error_code TEXT NOT NULL DEFAULT '' CHECK (length(last_error_code) <= 64),
    UNIQUE(event_id,channel),
    CHECK ((status='leased') = (lease_token IS NOT NULL AND leased_until IS NOT NULL))
);
CREATE INDEX aegis_notification_jobs_ready_idx
    ON aegis_notification_jobs(next_attempt_at,created_at,id)
    WHERE status IN ('pending','retry','leased');
CREATE INDEX aegis_notification_jobs_event_idx
    ON aegis_notification_jobs(event_id);
CREATE TABLE aegis_notification_attempts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id UUID NOT NULL REFERENCES aegis_notification_jobs(id),
    attempt_number INTEGER NOT NULL CHECK (attempt_number BETWEEN 1 AND 8),
    outcome TEXT NOT NULL CHECK (outcome IN ('sent','retry','dead')),
    error_code TEXT NOT NULL DEFAULT '' CHECK (length(error_code) <= 64),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(job_id,attempt_number)
);
