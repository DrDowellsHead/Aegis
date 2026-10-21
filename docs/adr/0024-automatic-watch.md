# ADR 0024 — Automatic incident evaluation (step 5.7)

Status: implementation in `develop`. The original roadmap ended phase 5
at 5.6. Step 5.7 was added to make incident detection independent of manual
button presses and to complete the end-to-end monitoring loop.

## Scope

The optional, separately supervised `aegis watch` process periodically
evaluates *all registered agents* against the existing CPU/RAM/disk
threshold rules and last committed heartbeat/telemetry contact time.
It **reuses** `EvaluateRulesAs` and `EvaluateAgentLivenessAs`; no
duplicate business rules or parallel in-memory incident state are added.
The same PostgreSQL transactions, active-incident uniqueness constraints,
cooldown, escalation and 5.5 outbox remain authoritative.

Default `--interval=30s` measures the pause **after** a full cycle.
Default `--offline-after=1m` determines contact staleness based on
server-side receipt time, not the remote machine clock or power status.
Interval must be 10 seconds–1 hour, offline threshold 10 seconds–24 hours,
both integral seconds. `--once` evaluates one fleet cycle for smoke
tests or manual automation; normal mode repeats until graceful shutdown.

Rules with stale or insufficient telemetry preserve their previous
state and are not wrongly escalated. An agent never seen online is
`unknown`, not offline. Alert detection requires the metric to be
present in the configured evaluation window. A burst that starts and
ends between checks may be missed.

## Concurrency and safety

A watch process pins a PostgreSQL connection and acquires session-level
`pg_try_advisory_lock(176341,57)` for the **entire fleet cycle**.
A second process skips a competing cycle; no conflicting write to the
watch status table occurs. PostgreSQL releases the session lock if the
connection/process crashes. The same connection unlocks it explicitly
before returning to the pool; if unlock fails, the connection is
discarded. This is a cooperative leader lock among watch processes,
not a network partition-proof distributed consensus mechanism.

Individual rule rows and agent rows continue to be locked by the
original business transactions, so concurrent manual web evaluations
are serialized at the source. PostgreSQL partial unique indexes prevent
multiple active incidents for the same rule or agent connectivity source.

The cycle pages through registered agents using UUID keyset pagination
(`BatchSize=100`) to avoid loading the fleet at once. It evaluates
rules and connectivity sequentially per agent, with per-call
timeouts and cancellation. An individual agent failure is counted
but does not starve the next agent. Fatal registry/page/progress errors
stop that cycle and cause a retry after the interval. No overlapping
cycles or catch-up burst after slow scans.

There is **no arbitrary maximum fleet size**; a very large fleet may
produce long cycles. Throughput, scheduling fairness, and scaling to
multiple shards belong to phase 6.

## Status and observability

Migration `014_automatic_watch.sql` adds one row in
`aegis_watch_status`. The active watch process persists start time,
progress after each agent page, last completion, last full success,
scan counts, errors, configured interval and offline threshold.
It is read through authenticated, read-only
`GET /api/web/watch/status` and shown on the Incidents page.
Both admin and viewer may read. No browser mutation route exists.

A `stale` flag is computed against **PostgreSQL clock time**.
No progress for more than `max(120 seconds, 3 × interval)`
means the status is stale, even when a crashed process left the row
in `running` or `ok`. A stale / never-started evaluator must not be
reported as running. The dashboard only refreshes this information
when the Incidents page loads or the user refreshes it.

Scheduled checks are identified by the internal actor `scheduler`
and deliberately do **not** append a manual operator audit row for
every scanned agent. Actual incident transitions still write their
existing event and new activity audit rows in the same transaction.

## Deployment and failure handling

1. Back up PostgreSQL and run `aegis migrate` before restarting
   any Aegis process; `Ready` now requires migration 014.
2. Run `aegis watch --interval 30s --offline-after 1m` continuously
   under a supervisor (systemd, Windows service wrapper, launchd).
3. Run `aegis notify` separately with configured delivery channels.
   The watch process detects/opens incidents; notification workers
   deliver existing outbox jobs. One does **not** launch the other.
4. Verify the Incidents page displays recent watch activity without
   `stale` and that notification delivery is healthy.

Process stop, PostgreSQL outage and timeout are not silent success:
there is either a failure/partial-failure result or an eventually
stale heartbeat. A supervisor should restart exited processes and
monitor staleness independently. No external alert can be guaranteed
while **both** the server/database and the watcher are down.

Live Telegram/SMTP delivery, host installer integration, multi-node
HA and unattended startup on reboot are separate operational tests
and phase-6/7 tasks. A successful CI build alone is not a production
availability claim.

## Security and limitations

Only registered agents are visited. The watch service uses the server
database identity; it is never exposed to public HTTP. Secrets remain
in environment-managed configuration, not the route, logs or journal.
The watch status endpoint reveals aggregate counts, not credentials
or private topology. SQL writes use parameterized queries and
transaction boundaries already established in phases 3 and 5.

Periodic evaluations can generate repeated alerts if policy cooldown
is too small, so choose a detection interval and cooldown appropriate
to the fleet. The worker does not promise exact deadlines, event-time
reconstruction, or discovery of faults shorter than a scan interval.
