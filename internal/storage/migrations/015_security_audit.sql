-- 015: durable, independent security audit. Do not include IP, password, token,
-- session cookie or arbitrary browser-supplied text.
CREATE TABLE aegis_security_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    action TEXT NOT NULL CHECK (action IN
       ('login_ok','login_failed','login_throttled','logout',
        'sessions_revoked','admin_denied')),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 32),
    subject TEXT NOT NULL DEFAULT '' CHECK (length(subject) <= 32),
    role TEXT NOT NULL DEFAULT '' CHECK (role IN ('','admin','viewer')),
    affected_sessions INTEGER NOT NULL DEFAULT 0 CHECK (affected_sessions BETWEEN 0 AND 128)
);
CREATE INDEX aegis_security_events_actor_id_idx ON aegis_security_events(actor,id DESC);
CREATE INDEX aegis_security_events_action_id_idx ON aegis_security_events(action,id DESC);
