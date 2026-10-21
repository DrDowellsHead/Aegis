# ADR 0029 — bounded ingress and delivery rates (Phase 6.5)

Status: implemented in `develop`; CI and real load testing are separate gates.

## Why the existing limits are insufficient

`grpc.MaxConcurrentStreams(64)` applies to each HTTP/2 connection, not to all
connected agents together. The PostgreSQL pool is already bounded to 10 open
connections; without application-level admission, many RPC handlers can
wait behind it, retaining request payloads and goroutines.

## Receiver admission

`aegis receive` now accepts:
- `--max-inflight-samples` (default 8, range 1..64): total in-flight sample
  handlers admitted for validation and persistence across all connections.
- `--max-per-agent-samples` (default 2, range 1..8, cannot exceed global):
  concurrent samples from one authenticated agent.
- `--max-inflight-heartbeats` (default 2, range 1..16): separate global
  concurrency pool for authenticated heartbeat persistence.

Authentication happens before admission. Full capacity returns gRPC
`ResourceExhausted` immediately (no unbounded waiting queue).
Cancellation is honored. Permits are released on every response, including
invalid samples, database errors and canceled requests. No overload reply
contains a durable acknowledgement. The agent's existing retry policy handles
ResourceExhausted as transient and reuses its durable spool ID. A direct
one-shot `send` reports the error; only `forward` provides persisted retry.

These limits protect application work, not raw TCP connections, gRPC/TLS
handshakes or OS file descriptors. Max message size remains 1 MiB; PostgreSQL
uses its pre-existing fixed 10-connection pool. Actual server sizing must
be established by Phase 6.7 load tests, not theoretical extrapolation.

## Notification worker pacing

`aegis notify --max-deliveries-per-minute` defaults to 60 (1..600).
Each worker sends serially as before. It waits *before* claiming the next
notification lease, so throttling does not consume a 45-second lease.
Idle polls do not consume permits; provider errors still count as attempts.
The timer respects context cancellation. Work remains in the PostgreSQL
outbox while the worker is rate-limited. No notifications are silently evicted.

The setting is per worker: multiple independently configured worker
processes have separate pacing budgets. This is NOT an installation-wide
rate limit or a finite total-size quota on the persistent PostgreSQL outbox.
Existing noise suppression and retention remain important; monitoring
backlog sizes and chaos/load testing are follow-up work.

## Verification and postponed real-world testing

Unit tests cover admission rejection, freeing permits, heartbeat
isolation, validation, cancelable pacing, and stable ID retries after
ResourceExhausted. CI covers Go race/vet/portable builds, PostgreSQL
integration and web tests on supported operating systems.

Before stable release, verify real-world OS and disk behavior: Windows
ACLs, process crashes, actual disk exhaustion, power failures, OS service
shutdown and restart, network loss/ACK ambiguity, PostgreSQL backup and
restore, and upgrades across supported machines.
