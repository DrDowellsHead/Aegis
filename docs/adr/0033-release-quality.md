# ADR 0033 — Release quality, supply chain and reliability targets (phase 6.9)

Status: implemented in `develop`; CI must pass, and real release criteria
remain provisional until staged load and platform testing.

## Supply-chain policy

- All referenced actions are pinned to **full commit SHAs** resolved from
  their official GitHub action repositories. Review changes from Dependabot
  before updating pins. Tags alone can move.
- Both CI and tag-release workflows have top-level `contents: read`.
  Only the draft-publication job receives `contents: write`. Checkout
  credentials are not persisted. No `pull_request_target`, no untrusted
  workflow triggers and no automatic Dependabot merging.
- The audit job runs `go mod download; go mod verify`, the pinned
  `govulncheck@v1.9.0 ./...` against the current Go vulnerability database,
  `npm ci --ignore-scripts` followed by `npm audit --audit-level=high`.
  Advisory findings should fail CI rather than being ignored.
  Go vulnerability analysis is reachability-aware, but is not a
  comprehensive proof of absence of malicious or insecure code.
- Dependabot may open small reviewable PRs into `develop` for Go, npm and
  GitHub Actions. The owner reviews changes, license effects, tests and
  advisories, then promotes stable code to `main` through a PR.
- Packaging already produces `SHA256SUMS`; CI and the release draft
  workflow now verify hashes independently before publishing. SHA256
  detects alteration but **does not authenticate who built an archive**.
  Signed provenance, artifact attestations and platform signing remain
  phase 7.6 and MUST NOT be claimed as implemented here.

## Runtime least privilege (operator checklist)

1. Use a dedicated unprivileged service user, no root/Administrator or
   interactive personal account; no blanket CAP_NET_ADMIN or Docker-socket
   permissions. Agent metrics do not require privilege escalation for the
   supported baseline collectors.
2. Limit secrets to the service account: Unix 0600 files/0700 directories,
   restrictive Windows ACLs (verify on actual Windows), no secrets in CLI
   arguments, logs, web bundles, backups or Git.
3. Bind diagnostic pprof/Prometheus only to numeric loopback and require
   a separate long token. Restrict access via firewall, do not reverse
   proxy pprof publicly. Restrict PostgreSQL listen/TLS as appropriate.
4. Provision separate PostgreSQL roles for regular application DML,
   migrations and backup/restore where permissions allow. A production
   DML role must not be SUPERUSER, CREATEDB, CREATEROLE or REPLICATION.
   Audit grants in phase 7 deployment; do not infer enforcement from CI.
5. Use isolated writable state, spool and log directories. Filesystem
   service hardening (systemd ProtectSystem/ReadWritePaths, launchd
   sandboxing and Windows service ACLs) belongs to phases 7.2–7.4.

## Service level indicators (what is measurable now)

Prometheus counter rates are valid only when the metrics endpoint was
scraped continuously, allowing for counter resets and enough observations.
No traffic => insufficient evidence, NOT 100% availability.

| Indicator | Prometheus or verification source |
| --- | --- |
| Durable telemetry request success ratio | `sum(rate(aegis_grpc_requests_total{method="send",code="OK"}[5m])) / sum(rate(aegis_grpc_requests_total{method="send"}[5m]))` |
| Telemetry p95 server-side latency | `histogram_quantile(0.95, sum by (le) (rate(aegis_grpc_request_duration_seconds_bucket{operation="send"}[5m])))` |
| SQL persistence failure ratio | `sum(rate(aegis_db_operations_total{operation="save",result="error"}[5m])) / sum(rate(aegis_db_operations_total{operation="save"}[5m]))` |
| Pool wait pressure | `rate(aegis_sql_pool_wait_duration_seconds_total[5m])` and `rate(aegis_sql_pool_wait_count_total[5m])` |
| In-flight saturation | `aegis_grpc_inflight_requests` and `aegis_grpc_requests_total{code="ResourceExhausted"}` |
| Lost measurements and dedup correctness | CI chaos tests, offline spool counts and durable DB row audit |

No Prometheus service is required or installed by Aegis. These time-series
do NOT detect total outages when the endpoint cannot be scraped; use
external blackbox probing in deployment. A server response `OK` records
durable database ACK to the server, not end-user display freshness.
Do not confuse these metrics with agent CPU/RAM observations.

## Provisional SLO candidates — NOT achievements

Targets to *evaluate*, not advertise, over a rolling 30-day window
after deployment instrumentation and 7.8 physical tests:

- Durable `Send` request success ratio >= **99.9%** for measured
  attempts under a documented supported load envelope, with overload
  `ResourceExhausted` counted as not successful for the attempt.
  This is not a substitute for external uptime probes.
- p95 server processing time for `Send` <= **250 ms** at nominal
  documented capacity with a healthy local PostgreSQL.
- PostgreSQL persistence error ratio < **0.1%** under healthy operation,
  excluding operator-triggered planned migration windows only when
  declared in advance.
- Restore drill: a verified backup must restore into an empty DB and
  pass schema readiness plus sampled row checks before release.
- Zero duplicate logical rows after lost ACK replay in the chaos tests.
  This is a correctness invariant, not a statistical SLO.

A 99.9% request success rate corresponds to at most one failed attempt
per thousand requests, not 99.9% time availability. Statistical
confidence, maintenance windows, traffic mix, data retention and
observation coverage must be reported when making any SLO claim.

## Capacity verification (not an invented requests-per-second number)

The built-in **concurrency limits** are 8 simultaneous measurement
writes, 2 per agent and 2 separate heartbeats by default, capped
globally at 64/16 respectively by flags. These are NOT throughput
promises. Phase 6.7 tests rapid rejection under 24 competing RPCs,
not maximal sustained RPS.

Before RC on the intended single VPS:

1. Capture host CPU/RAM and PostgreSQL resource limits and versions,
   recorder configuration and round-trip latency to the agent.
2. Ramp at fixed 1, 2, 4, 8, 16, 32 agents, each sending at the planned
   collection interval, keeping the real TLS and PostgreSQL topology.
   Run >=15 minutes per level, then a 60-minute sustained test at
   the proposed nominal capacity. Count retries, queue size and
   `ResourceExhausted` honestly.
3. Record 5-minute/1-hour p95 response, failure ratio, DB pool wait,
   Go heap and goroutines, external availability and durable ACK gap.
   If p95/error targets fail, record last passing workload and lower
   supported agent/RPS limits, tune under controlled profiling, then
   rerun. Do not silently raise limits to hide backpressure.
4. Repeat during PostgreSQL restart, network interruption and disk
   pressure and after rolling service restart without duplicate rows.
   Store results alongside release notes.
5. Restrict network access to diagnostics and PostgreSQL. Run real
   Windows/macOS/Linux service install, ACL and system shutdown tests
   before stable release (phase 7.8).

## Release gate

CI green on all three OSes + real PostgreSQL restore + dependency scan
+ pinned actions + checksum verification are mandatory automated gates.
A security advisory is a hard failure until dependencies or code are
updated. RC/stable additionally require tested minimal runtime grants,
a documented measured capacity envelope, signed artifacts where
supported, and the deferred physical recovery/install drills.
