# ADR 0021 — Noise suppression, cooldown, escalation (step 5.4)

Status: implemented in `develop`, **manual evaluation only**.

## Policy

Migration 011 adds a singleton persisted PostgreSQL row
`aegis_incident_settings`:
- `cooldown_seconds` default **300**, range **0–86400**. Setting 0
  disables cooldown completely.
- `escalate_after_seconds` default **900**, range **60–604800**.

Admin/local operator can view and edit these fields at /incidents.
A viewer may view the policy but cannot edit. Policy updates require
authenticated same-origin POST, with strict integer bounds on both
the gateway and PostgreSQL. Settings apply to the entire fleet and
are read on every evaluation. Changes affect existing incidents at
their next evaluation, without restarting Aegis.

## Cooldown and deduplication

One active incident is already enforced by partial unique indexes
from 5.3. When an evaluated source remains abnormal but has no active
incident, find its most recently **resolved** episode.

If `evaluation_time < resolved_at + cooldown_seconds`, suppress the
attempt to create another incident. The existing resolved incident
remains in history, while its `suppressed_checks` increases and
`last_suppressed_at` is updated. Repeated evaluations cannot create
duplicate active incidents. At the first evaluation **after** the
cooldown expires, if the condition is still abnormal, create a fresh
incident UUID.

Cooldown applies to both operator resolutions and *natural*
recoveries (including flapping conditions). If the condition recovers
and then fails again within cooldown, it remains suppressed until
the cooldown ends. **This is intentional noise reduction, not an
indication of recovery or health.** Operators must choose cooldown
according to their risk tolerance.

The absence of fresh metrics does not imply recovery: an incident
remains active if the rule latch remains firing on insufficient data.
Unknown never-connected agents do not produce connectivity incidents.

## Escalation

A newly opened incident has `severity=warning`. If it stays
`state=open` for at least `escalate_after_seconds` and the abnormal
condition is **still present at evaluation**, it becomes
`severity=critical` with a single `escalated_at` timestamp
and one `escalated` transition event. Repeat evaluations cannot
duplicate this event.

Acknowledged and resolved incidents do **not** escalate automatically.
Acknowledgement after escalation retains the critical severity (it
does not erase the fact of escalation). A new episode always starts
at warning.

Escalation is a **priority classification**, not an email/Telegram
message. Delivery will be implemented in step 5.5.

## Atomicity and synchronization

Every rule or contact evaluation uses one SQL transaction that
synchronizes the condition state, incident state, suppression count,
escalation event and associated timestamps. The source's rule/agent
row lock serializes competing evaluations. Policy row reads use
`FOR SHARE` to prevent half-applied updates during a transaction.

For rule evaluations, the authoritative clock is read from PostgreSQL
*after acquiring rule locks*. For agent connectivity evaluations,
the server clock is also read inside the agent-row lock.

The old 5.3 integration tests explicitly disable cooldown to check
the former immediate-reopening behavior. New PostgreSQL tests cover
cooldown after manual resolution, cooldown after natural recovery,
escalation once, no escalation after acknowledgment, bounds and
durable policy updates.

## API

- `GET /api/web/incidents/settings` for admin and viewer.
- `POST /api/web/incidents/settings` for admin/local operator only,
  body `{"cooldown_seconds":300,"escalate_after_seconds":900}`.

The existing incident list now also returns:
`severity`, `escalated_at`, `suppressed_checks` and
`last_suppressed_at`. Timeline events may now include
`action=escalated`.

## Operational limits

**No background scheduler is present in 5.4.** The policy and
escalation are applied **only when** a threshold rule or agent
connectivity is manually evaluated, as in step 5.3. A problem can be
overdue for escalation without its database row changing until the
next evaluation. Future step 5.5 delivers notifications; periodic
evaluations require a separate supervised server worker.

Global cooldown may temporarily hide a real repeated outage.
Current counters record *evaluation attempts suppressed*, not the
number of actual outages or messages. No actual notifications exist
at this stage. This does not implement a full alert-routing policy,
acknowledgment deadline or multi-level escalation chain.
