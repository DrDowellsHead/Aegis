# ADR 0020 — Incident lifecycle (step 5.3)

Status: implementation in `develop`; automated scheduling, notification delivery, and noise suppression are later steps.

## Contract

An **incident** is a persistent episode of an abnormal condition, not a single
metric sample. Its identity persists across evaluations and acknowledgments.

State machine:

```
normal condition --breach--> open --operator--> acknowledged
                              |                      |
                        recovery / manual     recovery / manual
                              |                      |
                              +-------> resolved <----+
```

`acknowledged` means that an operator has noticed the problem; it does
**not** indicate recovery. The only terminal state is `resolved`. A new
breach after recovery creates a *new UUID*, preserving the previous
incident and all of its events. A second acknowledgment or action against
an already-resolved incident returns HTTP 409, preventing stale updates.

There are two incident kinds:
- `threshold` keyed by the original threshold rule UUID;
- `agent_offline` keyed by the registered agent UUID.

An agent that has **never** connected is `unknown`, not offline; its
absence cannot create a false loss-of-connection incident. Presence
evaluation uses PostgreSQL server receipt time from step 5.2 and a
configurable threshold (10 seconds to 24 hours, default 1 minute).
The `offline` state means missing contact, **not** proof that hardware
is switched off.

## Transactional invariants

Migration `010_incidents.sql` creates incident rows, their minimal
append-only transition events and partial unique indexes that allow
at most one non-resolved incident for each rule or connectivity source.

For a threshold rule, the current 5.1 evaluation transaction already
locks the rule and its latch. Step 5.3 now creates or resolves its
incident **inside that same transaction**. An aborted evaluation cannot
produce an orphaned incident or a transition without its rule state.
The database partial unique indexes and the lock protect against
simultaneous evaluations.

Evaluation with missing/stale data preserves the previously firing
latch and its current incident. Insufficient data does not imply
recovery. Deleting a rule automatically resolves its active incident
with reason `rule_deleted`, then deletes the rule; the incident
survives with a nullable rule foreign key and a snapshot title.

A separate connectivity evaluation locks the registered agent row
(`FOR UPDATE`), serializing it with heartbeat and telemetry receipt
updates. Offline opens/keeps one incident, restored contact resolves it.
These checks also commit their transition events together with
incident state. No inference is made from the agent's own clock.

Step 5.3 also migrates already-firing 5.1 rules into open incidents,
preserving prior transition timestamps when available.

## Operator actions

`open → acknowledged`: admin/local operator only, with
`acknowledged_at` and `acknowledged_by`. Any subsequent check that
still detects the problem leaves the acknowledgement intact.

`open|acknowledged → resolved`: automatic on recovery; also available
as a deliberate manual override. A manually resolved incident is
*not silenced*: if the same abnormal condition remains at the **next**
evaluation, a **new** open incident is created. Cooldown, suppression
and escalation belong to step 5.4.

All state changes and the minimal transition event are committed in one
PostgreSQL transaction. The event records action, operator/system
identity, reason and timestamp. This is **not yet** the comprehensive
searchable audit journal planned for step 5.6.

## Web gateway

Existing secure cookie sessions and Same Origin POST checks are reused.
`viewer` can list incidents and inspect transitions; `admin` or
the local single operator can evaluate connectivity, acknowledge, and
resolve. The backend is authoritative; UI control visibility is not
an access control boundary.

- `GET /api/web/incidents?state=active|resolved|all&agent_id=UUID&limit=50&cursor=...`
  paginated with an opaque descending timestamp/UUID cursor.
- `GET /api/web/incidents/events?id=UUID` returns up to 100 transition
  events for one incident.
- `POST /api/web/incidents/evaluate` with
  `{"agent_id":"UUID","offline_after":"1m"}` evaluates loss of contact.
- `POST /api/web/incidents/acknowledge` with `{"id":"UUID"}`.
- `POST /api/web/incidents/resolve` with `{"id":"UUID"}`.

The existing `POST /api/web/rules/evaluate` now also manages threshold
incident transitions automatically in the same transaction.

Input lengths, UUIDs, roles and Origins are checked in Go. Internal SQL
errors are logged by the server but not returned to browsers. Incident
list is limited to 100 items per request; pagination is keyset-based.

## Explicit limitations

This stage does **not** introduce periodic background evaluations.
An administrator must run the threshold rule evaluation on the Rules
page and/or contact evaluation on the Incidents page. Opening the
Incidents page alone never mutates state. Persistent background
detectors and alerting require scheduling with clear fleet locking
and operational failure handling, and are separate follow-ups.
Incidents are not notifications; email, Telegram, and webhooks are
planned for 5.5.

A transient problem that occurs and disappears between manual
evaluations will not be recorded. Incident timestamps indicate
the time *the server detected the episode*, not its guaranteed start.

Run `aegis migrate` using a migration-capable DB role before restarting
the gateway. The operator database role needs scoped INSERT/UPDATE/
SELECT privileges for the incident and event tables. Deploy review,
backup/restore and monitoring remain operator responsibilities.
