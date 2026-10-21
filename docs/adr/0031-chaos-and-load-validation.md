# ADR 0031 — Repeatable fault injection and bounded load (Phase 6.7)

Status: automated scenarios implemented in `develop`; CI validation required.

## Goals

Verify **at-least-once delivery and deduplication**, not exactly-once
network transmission. A local disk record is not acknowledged until the
client receives a durable response from the authenticated receiver.
Chaos tests must use short, bounded deadlines and isolated temporary files
so a failed scenario cannot exhaust CI or corrupt production environments.

## Fault matrix

| Injected fault | Expected invariant |
| --- | --- |
| Agent cannot reach receiver; parent context cancels | Existing spool records survive shutdown/reopen and replay with their original IDs |
| First response disappears after the mock sender commits | Retry uses the same measurement ID; one logical record; spool drains only on success |
| PostgreSQL Save returns an injected error before writing | gRPC Unavailable; no inserted measurement |
| PostgreSQL transaction commits, but receiver's Save wrapper returns an error | gRPC Unavailable, exactly one real PostgreSQL row; later retry deduplicates |
| gRPC receiver is gracefully stopped between attempts | Request fails without ACK; restart and replay leave exactly one row |
| 24 simultaneous TLS-authenticated RPCs while the only Save permit is held | All excess RPCs reject with ResourceExhausted without additional Save calls |
| Heartbeat while measurement capacity is saturated | Separate heartbeat pool remains usable; permits released after requests complete |

These scenarios exercise real TLS/gRPC, Go goroutines and, where configured,
PostgreSQL 17 with real SQL commits. Intentional faults are inside test-only
wrappers. **They do not** claim to have physically killed the database or
server process, disconnected a network cable, filled a production disk or
simulated power loss. Those destructive and performance-capacity measurements
are deferred to pre-release physical testing (Phase 7.8).

## CI

Normal cross-platform Go tests include the local spool and bounded ingress
scenarios. Linux CI's PostgreSQL integration job additionally repeats all
`TestChaos*` tests three times with the race detector. If the PostgreSQL
connection environment variable is absent, only the real-DB scenario is
skipped. All other tests run without external dependencies.

`go test -race` detects shared-memory races but does not prove the absence
of deadlocks, lost ACKs under every timing pattern, or a real-world
throughput limit. Phase 6.8 adds profiling and observability; Phase 6.9
sets measurable SLI/SLO criteria after load measurement.
