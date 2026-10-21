# ADR 0019 — Independent authenticated heartbeat (step 5.2)

Status: implemented in `develop`. No incident lifecycle or automatic alerts yet.

## Motivation

Previously only a committed telemetry request updated
`aegis_agents.last_contact_at`. Collector stalls, an OS-metric error,
long sample intervals and a growing disk spool could make a functioning
agent appear offline. Presence and CPU/RAM/disk values are not the same
signal.

## Contract

Extend `aegis.telemetry.v1.TelemetryService` with a unary
`Heartbeat(HeartbeatRequest) -> HeartbeatResponse`. The payload contains
**only agent_id**. It carries no timestamp, network counters, OS metrics
or secret. The per-agent Bearer token comes from existing TLS-protected
gRPC metadata, exactly like `Send`. On the server:

1. Validate the agent ID and authorize its Bearer token against the
   authoritative private JSON registry **before** accessing PostgreSQL.
2. Atomically UPDATE the already **registered** `aegis_agents` row,
   setting `last_heartbeat_at` and monotonic `last_contact_at` using
   PostgreSQL's server clock. Never INSERT an unknown agent.
3. Return `durable=true` only if the SQL UPDATE has committed.
   The client rejects empty or non-durable acknowledgments.

A bad token is `Unauthenticated`; missing DB registration is
`FailedPrecondition`; database errors become `Unavailable`
without disclosing database credentials to agents.

The existing `Send` RPC still writes measurements and records
`last_telemetry_at` and `last_contact_at` in **the same transaction**.
An authenticated deduplicated replay updates the receipt time as before.
Heartbeat does not change `last_sample_at`, OS, architecture or any
measurement history.

## Agent behavior

The long-running `aegis forward` command now runs an independent
heartbeat loop over its existing TLS gRPC client connection. Default
`--heartbeat-interval=15s`; allowed range 1 second to 1 hour; `0`
disables heartbeat for compatibility with servers not yet upgraded.
The first heartbeat is sent immediately on the separate worker,
subsequent ones every interval. Each call has the existing bounded
`--request-timeout` and respects context cancellation. A heartbeat
failure logs a warning and **never deletes or mutates** the telemetry
disk queue. Telemetry retries maintain their original IDs and durable
ACK rules. The worker is cancelled and joined on forwarder shutdown;
there is no leaked goroutine.

One-shot `send` sends telemetry only. Legacy `agent` locally collects
without a network connection. Upgrade the server first, then agents.
Older servers return `Unimplemented` for Heartbeat; disable the
feature with `--heartbeat-interval=0` until upgraded. New server
continues to accept old clients' telemetry without heartbeats.

## Database and status semantics

Migration `009_heartbeat.sql` adds:
- `last_heartbeat_at` nullable server timestamp
- `last_telemetry_at` nullable server timestamp, backfilled from
  legacy `last_contact_at`

`last_contact_at` remains the freshest **committed authenticated
heartbeat OR telemetry** received by the server. For the configurable
dashboard threshold (10 seconds–24 hours):

- `unknown`: neither signal ever committed.
- `online`: most recent contact is within the configured duration.
- `offline`: contact exists, but is older than the duration.

These states describe **server reachability evidence**. An offline
status cannot prove that the actual computer is switched off: a
firewall, ISP failure, sleeping process or unreachable database can
yield the same symptom. Likewise, an online heartbeat proves only
that a valid authenticated agent reached the server recently.

The dashboard lists last contact, last heartbeat and last telemetry
separately, with nullable times. The list and summary use PostgreSQL
server timestamps, not device clocks; historic telemetry replay must
not fabricate fresh measurement times.

## Limitations and safety

There is no automatic incident creation/acknowledgment/resolution
until 5.3, and no notifications until 5.5. Long-term event history of
individual heartbeats is deliberately not stored; only latest receipt
timestamps are maintained. A failed heartbeat is **not queued**:
the next successful one is enough to re-establish presence. DB
writes are one row per heartbeat and per registered agent; operators
must size the interval for their fleet and PostgreSQL capacity.

Do not set the offline threshold below normal inter-heartbeat duration
plus reasonable latency, jitter and outage tolerance. With a 15s
heartbeat interval, the default 1-minute dashboard threshold provides
roughly four opportunities before showing offline. Agent crashes,
machine sleep and network partitions are not distinguishable without
additional system-level evidence.

Migration requires write privileges. Run `aegis migrate` before
restarting `receive`/dashboard. Keep a backup of the PostgreSQL DB
and do not expose bearer tokens or operator credentials in frontend.

## Verification

Go: receiver rejects missing/invalid tokens, only acknowledges a
committed heartbeat, does not leak PostgreSQL failures; agent loop
cancels promptly and does not touch the spool; real TLS gRPC/Pg
integration observes both receipt clocks separately and rejects
unauthorized calls. PostgreSQL: brand-new registered agent is
unknown, heartbeat alone makes it online without a measurement;
stale contact makes it offline; telemetry updates its own time.
React + Playwright: last heartbeat and telemetry are presented
separately, including a live agent with no metrics.
