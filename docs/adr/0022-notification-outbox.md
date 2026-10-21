# ADR 0022 — Transactional notification outbox (step 5.5)

Status: implemented in `develop`. **Incident evaluation is still manual**;
notification delivery itself is automatic while `aegis notify` is running.

## Transactional contract

Migration **012_notification_outbox.sql** adds:
- `aegis_notification_routes`: at most one enabled non-secret destination
  for each channel (email, Telegram, webhook).
- `aegis_notification_jobs`: immutable event/destination snapshot, unique
  `(event_id,channel)`, delivery state, retry schedule, lease and attempt count.
- `aegis_notification_attempts`: committed outcome of each completed attempt.

The existing `incidentEvent` function inserts a transition event and,
**in the same PostgreSQL transaction**, queues one job per enabled channel
for `opened`, `escalated`, and `resolved`. Acknowledgements are
audit-only. An aborted incident transaction cannot leave a phantom
notification; a failed external service never rolls back an incident.
Events created before a route was configured are **not backfilled**.

The separate `aegis notify` worker first configures its destinations
(only non-secret identifiers stored in PostgreSQL), then claims due jobs
using `FOR UPDATE SKIP LOCKED` and a 45-second lease token.
The worker has a 12-second network timeout. It records delivery outcome
and appends its attempt journal **atomically**. It refuses stale lease
results and recovers expired leases after crashes. Eight attempts
maximum, exponentially backed off (15s, 30s, ... maximum 1h).
Permanent HTTP failures (except 429) do not retry. Exhausted jobs
become `dead`. A crash on the eighth lease is also marked `dead`.

Delivery is **at least once**, not exactly once. If an external
service accepted a message but Aegis crashed before committing success,
it may send again. Webhook callers should deduplicate with
`Idempotency-Key: <event-id>-webhook`, Telegram/email receivers
cannot be assumed to deduplicate reliably. Never use notification
counts as a count of unique incidents.

## Channels

- Email: SMTP with *implicit TLS* and verified certificate, no
  plaintext fallback. One recipient configured through `--email-to`,
  SMTP host:port and From set as flags, credentials in
  `AEGIS_SMTP_USER`, `AEGIS_SMTP_PASSWORD`. A provider that only
  offers STARTTLS port 587 requires a future explicit transport.
- Telegram: Bot API `sendMessage`, chat ID stored in route table,
  `AEGIS_TELEGRAM_TOKEN` only in the worker environment.
- Webhook: public HTTPS destination on port 443, no URL credentials,
  query tokens, or redirects. Actual TCP dial checks resolved IP to
  reject private, loopback, link-local and reserved ranges, including
  DNS-rebinding. `AEGIS_WEBHOOK_SECRET` (at least 32 bytes) signs the
  JSON body with HMAC SHA-256 in `X-Aegis-Signature`. The receiving
  system must validate the signature, authenticate source as needed,
  and deduplicate the event key. Network response bodies are discarded.

Only error categories (e.g. `remote_retry`, `network_error`,
`smtp_auth`) reach the audit table. No SMTP password, bot token,
webhook HMAC secret, full HTTP URL, or remote response body is logged.
The browser shows aggregate counts pending/retry/leased/sent/dead.

## Deployment / operational rules

Run `aegis migrate` first. Start **one** `aegis notify` process
with one or more configured channels *before* evaluating rules.
If a notification route is changed or removed, already enqueued
jobs for an older target are refused as `route_changed`, avoiding
accidental send to a stale recipient. Multiple concurrent workers must
have the **same** configuration and secrets; do not use independent
conflicting route configurations on one database. Workers may compete
safely for leases with matching config.

The worker is separate from `aegis receive` and dashboard. Manage
it with systemd, Windows service manager, or another supervisor.
Graceful shutdown stops polling; an unconfirmed in-flight attempt
is reclaimed after its lease expires. Do not store secrets in Git,
command line flags, React assets or GitHub Actions.

Email, Telegram, and webhook addresses/chats are configured by the
trusted server operator. This step intentionally supports one route
per channel (3 maximum) — no user-specific subscriptions.

**No automatic periodic incident detector has been implemented.**
Incidents are still created by pressing `Проверить сейчас` in
rules or `Проверить связь` in incidents; after that, the worker
will automatically deliver notifications. Automatic evaluator
scheduling, recurrence and missed-incident detection are a separate
task. This limitation is not masked as "24/7 monitoring".

## Testing

Pure tests cover outbound URL/private-IP rules, signature bytes,
stable idempotency key, HTTP status classification, and invalid
route parameters. PostgreSQL integration covers atomic inserts,
exactly one job per event/channel, no job on acknowledgment,
multiworker claims, retry delay, permanent failure, lease expiration
on maximum attempts, and attempt journal. Browser tests cover safe
status-only read access. No real Telegram/SMTP production credentials
are used in CI.

## Limitations

Mail, Telegram and webhook delivery depend on third-party providers,
credentials, network policies and provider quotas; CI cannot guarantee
success with a live account. SMTP implicit TLS on port 465 is the
supported mode for now. A permanently failed job is *not* automatically
requeued by updating credentials; an operator will need a manual
replay tool in a later step or a database-guided intervention.
The complete actor audit and incident event browsing are refined in 5.6.
