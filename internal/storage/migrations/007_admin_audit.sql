-- Operator audit rows contain only allowlisted metadata; no DSNs or secrets.
-- Progress is committed in the SAME transaction as each maintenance batch.
CREATE TABLE aegis_admin_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action TEXT NOT NULL CHECK (action IN ('retention','backfill')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 64),
    scope TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','succeeded','failed','interrupted')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    affected_rows BIGINT NOT NULL DEFAULT 0 CHECK (affected_rows >= 0),
    retention_days INTEGER,
    CHECK ((status='running' AND finished_at IS NULL) OR
           (status<>'running' AND finished_at IS NOT NULL))
);
CREATE INDEX aegis_admin_audit_recent_idx
    ON aegis_admin_audit (started_at DESC, id DESC);
