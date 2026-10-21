# ADR 0016 — Browser refresh resilience and E2E tests (4.5)

Status: implemented in develop, subject to CI.

## Problem

Aegis Dashboard re-requested JSON every 30 seconds, but the agent detail
component cleared data before every fetch, visibly removing charts.
Requests could hang or return transient 503. Moving between a page,
a cursor or an agent must not allow late results to overwrite new
selection. Client-only status simulation is not a substitute for browser
tests.

## Decisions

The local dashboard keeps a **last successful response** while refreshing
the **same query**. While waiting it labels the previous snapshot; if
retries fail it displays an error and offers a manual retry, without
turning missing values into zero. For a changed agent, disk, custom
time window, comparison mode or cursor it hides incompatible old data.
For moving presets, old graph points remain rendered against the
old successful time window and are explicitly labeled until a new
read completes. This distinction avoids misleading plots.

The fetch layer lives in `web/src/api/request.ts`. GET-only retry
policy: initial attempt + at most two retries for network failures,
request timeouts, and transient HTTP 408/429/5xx (400/401/403 do not
retry). Pauses: 350ms and 700ms; per-attempt timeout: 8 seconds.
`AbortController` cancels active network I/O and pending retry timers.
Login/logout POSTs are NEVER retried automatically.
The server remains the ultimate authority for session expiry.
No authentication token is written to the browser bundle or storage.

The fleet query records its filter+cursor key; data from an
earlier filter is hidden, not presented as a new result. Detail queries
track the current agent, period, disk selection and history cursor.
Overlapping effects discard completions from aborted requests.
The old data is kept for the *same semantic selection* only. On
document visibility return and browser `online` events, a refresh is
requested, in addition to the 30-second timer while visible.
A background failure does not imply the monitored host is offline.

## Testing boundaries

- Vitest unit tests cover 503 retry, 401/POST non-retry, cancellation,
  and maximum attempt count, alongside existing UTC and chart tests.
- Playwright Chromium executes browser UI E2E with mocked
  `/api/web/*` HTTP responses. It exercises login, agent detail,
  charts, temporary 503 and retry, session expiry, and mobile layout.
- These browser E2E tests do **not** run a real Go dashboard or
  PostgreSQL. Separate Go tests exercise the storage, request
  validation and auth. A future full-stack browser integration should
  provision a temporary DB, build the Go gateway with web assets, and
  assert the cookie+database flow end-to-end.
- Chromium is tested in CI; Firefox/WebKit and physical mobile
  browsers still require validation.

This phase does not open ports, add external auth, introduce WebSockets
or claim production readiness. Remote HTTPS/RBAC remains 4.6.
