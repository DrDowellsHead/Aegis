# ADR 0012 — Web dashboard: frontend boundary and API contract (step 4.1)

Status: **accepted as design**, not a claim that production web access exists.

## Decision

Build the future dashboard with **React + TypeScript + Vite** in `web/`.
Go remains the backend and PostgreSQL is never accessed by the browser.
Vite is the development/build tool, not a production API server. We keep
the frontend a client-side SPA for the initial release, with ordinary
semantic HTML and keyboard-accessible navigation.

Initial pages:
- `/` — Overview: counts/health after the data gateway is available.
- `/agents` — registered agents, online/offline/unknown and last contact.
- `/agents/:id` — agent measurements, time-series graphs, error states.
- `/incidents` — **future**: rule evaluation and incident lifecycle.
- `/settings` — **future**: session and operator settings.

The 4.1 scaffold deliberately displays **empty/unavailable** states instead
of fabricated monitoring readings or pretending to be connected to live data.
Navigation, responsive shell and reusable TypeScript data contracts are a
foundation for steps 4.2–4.6. Graphs and real data fetching are NOT 4.1.

## Existing contracts, verified against Go source

`GET /api/v1/agents`: `limit` 1..200, `cursor` UUID, `offline_after`
10s..24h in whole seconds; response includes `items`, `has_more`,
optional `next_cursor` and `offline_after`. `status` is
online/offline/unknown, based on last authenticated telemetry contact;
it does **not** prove machine power state.

`GET /api/v1/measurements`: required `agent_id`; optional
`from` inclusive, `to` exclusive, `limit` 1..100 (default 50),
and opaque `cursor`. Response includes `items`, `has_more`, optional
`next_cursor`; `snapshot` may contain missing CPU/memory/disk,
partial errors and network counters. Records are keyset-paginated,
without cross-request snapshot isolation.

`GET /api/v1/series`: required `agent_id,from,to,step`, optional
`disk_path`; `step` in 1m/5m/15m/1h/6h/24h, up to 90 days and
500 bins. Response contains `points` with per-metric sample counts,
nullable CPU avg/min/max, RAM avg and disk avg (only when disk_path
is specified). Empty bins are omitted. Raw network counters are NOT
network transfer speeds; a correct rates endpoint needs distinct
server-side delta calculations in a later phase.

Authoritative schema: `api/openapi/history-v1.yaml`.
Frontend DTOs: `web/src/api/types.ts`. Keep both aligned with Go
`internal/history`, `internal/storage`, and `internal/metrics`.
Contract documentation is NOT new backend behavior.

## Security: boundary MUST be implemented before real dashboard data

Current `aegis history` is loopback-only and requires
`AEGIS_HISTORY_TOKEN` via Bearer. It is **not** a publicly deployable
browser backend. The token must NEVER be embedded in Vite env variables,
bundled JS, sessionStorage, localStorage, HTML, or browser requests.

Planned Go Web Gateway (a later step, prior to remote deployment):
1. Same-origin `/app` static bundle and browser-facing `/api` routes.
2. HTTPS, authenticated server-side sessions via Secure, HttpOnly,
   SameSite cookies; session rotation, login-rate limiting, logout.
3. Verify authorization for every API request; CSRF defenses on
   mutating endpoints; Origin/Host and security headers validation.
4. Server-to-server access using dedicated identities/DB read-only roles,
   not the agent registration token and not a publicly exposed
   `AEGIS_HISTORY_TOKEN`.
5. Separate administration and ingest permissions; audit operator actions.
6. No Vite reverse proxy to the protected history API in this step;
   no hardcoded token; no CORS wildcard.

Development preview of the static shell is NOT an authenticated dashboard.
Do not expose Vite dev server or the history API to an untrusted network.

## UX quality and error semantics

Responsive from 320px upwards. Keyboard navigation, accessible headings,
visible focus states, accessible labels, reduced motion and sufficient
contrast. The UI must distinguish: loading, empty, no metrics for time
range, missing metric (null), unknown/offline agent, server unavailable,
unauthorized/expired session. Never replace unknown with zero or mark an
agent as definitely powered off.

Tables use paged data, never eagerly fetch unlimited agent IDs/history.
UTC timestamps are carried as RFC3339 strings and displayed in the
viewer timezone (with full UTC on detail/tooltip as appropriate).
The UI must not extrapolate gaps. Use a chart library in step 4.3 only
when real data is safely accessible.

## Scope and acceptance for 4.1

- [x] Architectural decision and navigation/user flows
- [x] OpenAPI documentation of existing read-only endpoints
- [x] React/TS/Vite scaffold that builds and has empty states
- [x] No secrets or public data access; existing Go endpoint unchanged
- [x] Frontend production build checked in GitHub Actions
- [ ] Actual authenticated gateway (future; prerequisite for live UI)
- [ ] Agent list/graphs populated from live data (steps 4.2/4.3)
- [ ] Session management, incident editor and user settings (future)
