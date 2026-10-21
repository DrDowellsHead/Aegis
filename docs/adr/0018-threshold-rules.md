# ADR 0018 — Threshold conditions and hysteresis (step 5.1)

Status: implemented in `develop`. No alert incidents, background evaluation or notifications yet.

## Goal

Aegis already persists nullable percentages for CPU, RAM and each measured
filesystem (`004_chart_projections.sql`). Step 5.1 defines the condition
engine shared by future incident stages. Do not infer network throughput
from cumulative byte counters. Do not treat a missing metric as 0.

## Rules

Each enabled, registered agent may have up to 50 rules. A rule comprises:

- `name`, `agent_id`, `metric` = cpu/memory/disk.
- Exact `disk_path` for disk rules (other metrics MUST leave it empty).
- `direction` = above or below.
- `fire_at` and `recover_at` percentages in 0..100.
- `window_seconds` = 60..86400, `min_samples` = 1..10000,
  `max_age_seconds` = 1..window_seconds.

For `above`, the recovery threshold must be strictly **lower** than the
firing threshold; for `below`, strictly **higher**. This separation
is hysteresis. Example: fire on CPU average >=85%, recover <=70%.
Between 70 and 85 inclusive of neither boundary, keep the previous state.

Only `projected=TRUE` measurements whose **agent-reported collection**
time satisfies `at-window < started_at <= at` are included. The SQL
aggregation computes an arithmetic mean of individual valid sample
percentages, not a mean of already-aggregated chart buckets. Therefore
each sample has equal weight. For disk, all samples must have the exact
configured filesystem path. Identical packet retransmissions cannot be
counted twice because persistence deduplicates measurement IDs.

## Missing/late data

A rule is evaluated only when the number of non-NULL samples is at least
`min_samples` AND the newest sample is no older than `max_age_seconds`.
No data, incomplete backfill, missing metrics and stale samples produce
`last_status=insufficient_data`. The persisted `firing` latch is
**preserved**, neither opened nor recovered. A late packet can be
included if its measurement timestamp is inside the current window,
but an old sample does not magically become fresh because it was just
received by the database. A remote device with a badly drifting clock
may thus show insufficient data; use synchronized clocks.

## Persistence and concurrency

Migration 008 creates `aegis_threshold_rules` and
`aegis_rule_states`. `firing` is a boolean rule-level latch; it is **not**
yet an incident or an event for notification. Repeated evaluations
within unchanged conditions do not update `changed_at`. The
`evaluated_at` value is the latest evaluation time (UTC), distinct from
the original measurement timestamps. `last_average` is nullable.

Creation and initial state happen in one transaction; per-agent
registration lock prevents concurrent creation bypassing the cap. An
evaluation locks the matching rules and states in deterministic UUID
order (`FOR UPDATE`) and commits all new states atomically. If it fails,
no partial state is reported as committed. Data ingestion is unaffected:
a failed threshold evaluation cannot revoke an already durable ACK.

## Browser surface

At `/rules` operators can select a registered agent, create rules, list
results, and run **Проверить сейчас**. Viewers can read, but only
administrators (or the single local dashboard operator) can create,
delete, or evaluate. All mutation endpoints require same-origin POST
and an authenticated session. Server-side authorization remains the
authority; hiding controls in React is not sufficient.

API:
- `GET /api/web/rules?agent_id=UUID` -> `{items:[RuleRecord,...]}`
- `POST /api/web/rules` -> new rule
- `POST /api/web/rules/delete` with `{"id":"UUID"}`
- `POST /api/web/rules/evaluate` with `{"agent_id":"UUID"}`

Mutation operations require PostgreSQL INSERT/UPDATE/DELETE permissions
on the two rule tables. Existing read-only dashboard database accounts
from 4.6 must be granted **only** these scoped privileges in addition to
SELECT on telemetry and agent registry. Do not make the operator DB
account a superuser merely to support rule editing.

## Explicit limits

- Evaluation happens **only on an authenticated administrator's manual
  request**. No automatic scheduling, periodic detector or agent-side
  evaluation is implemented by this stage.
- `firing` is a condition, not an acknowledged incident. Lifecycle
  (open/acknowledged/resolved), event history and notifications follow
  in steps 5.3–5.6.
- Heartbeats / loss of contact will be introduced in 5.2; lack of
  samples currently means 'insufficient_data', **not** 'machine down'.
- Window checks use wall-clock time and reported timestamps. For a
  stable production detector, later stages must specify a schedule,
  checkpointing, distributed locking and tolerated clock skew.
- Rule evaluations are intentionally limited to one agent at a time;
  the browser has no expensive fleet-wide evaluate-all endpoint.

## Validation

Pure Go tests cover boundary equality, both directions, hysteresis and
stale/missing values; PostgreSQL tests verify durable transitions,
uniqueness, disk-path isolation and recovery; Go HTTP tests check
auth, RBAC, origin, bounded/validated requests and redaction; browser
E2E tests cover viewing and manually evaluating rules.
