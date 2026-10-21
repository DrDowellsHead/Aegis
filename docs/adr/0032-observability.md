# ADR 0032 — Receiver observability (Phase 6.8)

Status: implemented in develop; automated CI and release-host verification are separate gates.

## Scope

Aegis's telemetry receiver optionally exports *internal service health*
metrics. This is distinct from host measurements gathered by the agents.
The receiver owns one observability registry per process, not global
metrics shared across tests or unrelated instances.

`--diagnostics-listen 127.0.0.1:9091` enables an additional HTTP listener.
By default there is **no listener**. Numeric loopback is mandatory
(`127.0.0.1` or `::1`); wildcard and DNS names are refused. Every route
requires `Authorization: Bearer <AEGIS_DIAGNOSTICS_TOKEN>`, a separate
random secret of at least 32 bytes, configured through the environment.
Never proxy this HTTP port to the public internet. Anyone holding this
token can access memory/CPU profiles and may learn sensitive process
details. Use SSH tunnelling or scrape from localhost, not a public bind.

## Prometheus format

`GET /metrics` exports Prometheus text format 0.0.4 with these families:

- `aegis_grpc_requests_total{method,code}`: completed gRPC calls
- `aegis_grpc_inflight_requests`: currently handling gRPC calls
- `aegis_grpc_request_duration_seconds`: cumulative duration histogram
- `aegis_db_operations_total{operation,result}`: durable storage calls
- `aegis_db_operation_duration_seconds`: cumulative duration histogram
- `aegis_sql_pool_*`: open/idle/in-use connections, pool wait count/duration
- `aegis_go_*`: allocated heap bytes, goroutines, accumulated GC pause time
- `aegis_process_uptime_seconds`: elapsed time since registry initialization

Histogram bucket bounds are fixed, from 5 ms to 5 s plus +Inf. Counter
and histogram state is in memory and resets on receiver process restart.
A metrics scrape must NOT touch PostgreSQL; SQL pool metrics come from
`database/sql.DB.Stats()`. Timed DB operations encompass connection
pool waits, any underlying SQL statements and transaction COMMIT; this
does **not** claim to be a histogram for each individual SQL statement.
For individual query plans use `EXPLAIN (ANALYZE, BUFFERS)` in an isolated
environment or PostgreSQL statistics with restricted access.

Only a fixed set of `method`, `code`, `operation`, and `result` labels
is allowed. Do NOT add agent IDs, tokens, request paths, IP addresses,
SQL arguments, arbitrary error messages or user input as labels.

## pprof

Authenticated `/debug/pprof/`, `/debug/pprof/profile`,
`/debug/pprof/heap`, `/debug/pprof/goroutine`, and
`/debug/pprof/trace` use Go's built-in profiler. CPU profiling incurs
overhead; never run continuously. Heap snapshots may contain sensitive
memory. Prefer a one-off protected capture and secure deletion.
Diagnostics bind and lifetime are tied to the receiver's context.

## Operational example

```sh
export AEGIS_DIAGNOSTICS_TOKEN='<generate-a-random-secret-of-at-least-32-bytes>'
aegis receive --listen 127.0.0.1:9090 --tls-cert server.crt \
    --tls-key server.key --agents-file agents.json \
    --diagnostics-listen 127.0.0.1:9091
curl --fail -H "Authorization: Bearer $AEGIS_DIAGNOSTICS_TOKEN" \
    http://127.0.0.1:9091/metrics
```

For Prometheus use a `bearer_token_file` protected with mode 0600
and scrape only localhost; avoid embedding the secret directly in YAML.
For pprof, use a locally authorized HTTP client and capture output only
to secure files. This stage does not install or require a Prometheus
server, Grafana, a metrics database, or any paid service.

## Tests and remaining limits

CI tests authorization, denied non-loopback clients, label bounding,
concurrent observations, cumulative histogram invariants, gRPC and
database write timing, and cross-platform Go builds. Real memory leaks,
throughput thresholds, PostgreSQL query-plan regressions, long-running
profiling and TLS-secured remote collectors remain operational tests
for phase 6.9 and the final release.
