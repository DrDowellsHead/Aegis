-- Step 5.6: searchable append-only-by-contract activity feed.
-- Existing maintenance audit (retention/backfill) remains independent.
-- No tokens, passwords, URLs, request bodies or personally identifying IPs.
CREATE TABLE aegis_activity_log (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    category TEXT NOT NULL CHECK (category IN ('incident','operator','delivery')),
    action TEXT NOT NULL CHECK (
        action IN ('opened','acknowledged','resolved','escalated',
                   'rule_created','rule_deleted','rules_evaluated',
                   'connectivity_evaluated','policy_updated','routes_updated',
                   'sent','retry','dead')
    ),
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 80),
    agent_id UUID,
    incident_id UUID,
    reference_id TEXT NOT NULL DEFAULT '' CHECK (length(reference_id) <= 64),
    summary TEXT NOT NULL DEFAULT '' CHECK (length(summary) <= 160),
    details JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(details)='object' AND pg_column_size(details) <= 2048),
    source_event_id BIGINT UNIQUE REFERENCES aegis_incident_events(id),
    source_attempt_id BIGINT UNIQUE REFERENCES aegis_notification_attempts(id)
);
CREATE INDEX aegis_activity_agent_idx ON aegis_activity_log(agent_id,id DESC);
CREATE INDEX aegis_activity_incident_idx ON aegis_activity_log(incident_id,id DESC);
CREATE INDEX aegis_activity_actor_idx ON aegis_activity_log(actor,id DESC);
CREATE INDEX aegis_activity_time_idx ON aegis_activity_log(occurred_at DESC,id DESC);
CREATE INDEX aegis_activity_category_idx ON aegis_activity_log(category,id DESC);

-- AFTER INSERT triggers run inside the caller transaction, including outbox.
-- No second best-effort network or SQL write is necessary.
CREATE FUNCTION aegis_log_incident_event() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO aegis_activity_log
    (occurred_at,category,action,actor,agent_id,incident_id,
     reference_id,summary,details,source_event_id)
  SELECT NEW.occurred_at,'incident',NEW.action,NEW.actor,i.agent_id,
         NEW.incident_id,COALESCE(i.rule_id::text,''),i.title,
         jsonb_build_object('kind',i.kind,'reason',NEW.reason,
             'severity',CASE WHEN NEW.action='opened' THEN 'warning'
                  WHEN NEW.action='escalated' THEN 'critical' ELSE i.severity END),
         NEW.id
    FROM aegis_incidents i WHERE i.id=NEW.incident_id;
  RETURN NEW;
END $$;
CREATE TRIGGER aegis_incident_event_activity
AFTER INSERT ON aegis_incident_events
FOR EACH ROW EXECUTE FUNCTION aegis_log_incident_event();

CREATE FUNCTION aegis_log_delivery_attempt() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO aegis_activity_log
    (occurred_at,category,action,actor,agent_id,incident_id,
     reference_id,summary,details,source_attempt_id)
  SELECT NEW.occurred_at,'delivery',NEW.outcome,'notification-worker',
         i.agent_id,i.id,j.id::text,i.title,
         jsonb_build_object('channel',j.channel,'attempt',NEW.attempt_number,
                            'error_code',NEW.error_code),NEW.id
  FROM aegis_notification_jobs j
  JOIN aegis_incident_events e ON e.id=j.event_id
  JOIN aegis_incidents i ON i.id=e.incident_id
  WHERE j.id=NEW.job_id;
  RETURN NEW;
END $$;
CREATE TRIGGER aegis_delivery_attempt_activity
AFTER INSERT ON aegis_notification_attempts
FOR EACH ROW EXECUTE FUNCTION aegis_log_delivery_attempt();

-- Historical events retain their source identifiers. Backfill is deterministic
-- for existing rows. Historical severity is a best-effort snapshot; earlier
-- rule metadata/recipient information is deliberately not reconstructed.
INSERT INTO aegis_activity_log
  (occurred_at,category,action,actor,agent_id,incident_id,
   reference_id,summary,details,source_event_id)
SELECT e.occurred_at,'incident',e.action,e.actor,i.agent_id,i.id,
       COALESCE(i.rule_id::text,''),i.title,
       jsonb_build_object('kind',i.kind,'reason',e.reason,
           'severity',CASE WHEN e.action='opened' THEN 'warning'
                WHEN e.action='escalated' THEN 'critical' ELSE i.severity END),
       e.id
FROM aegis_incident_events e JOIN aegis_incidents i ON i.id=e.incident_id
ORDER BY e.occurred_at,e.id
ON CONFLICT (source_event_id) DO NOTHING;

INSERT INTO aegis_activity_log
  (occurred_at,category,action,actor,agent_id,incident_id,
   reference_id,summary,details,source_attempt_id)
SELECT a.occurred_at,'delivery',a.outcome,'notification-worker',
       i.agent_id,i.id,j.id::text,i.title,
       jsonb_build_object('channel',j.channel,'attempt',a.attempt_number,
                          'error_code',a.error_code),a.id
FROM aegis_notification_attempts a
JOIN aegis_notification_jobs j ON j.id=a.job_id
JOIN aegis_incident_events e ON e.id=j.event_id
JOIN aegis_incidents i ON i.id=e.incident_id
ORDER BY a.occurred_at,a.id
ON CONFLICT (source_attempt_id) DO NOTHING;
