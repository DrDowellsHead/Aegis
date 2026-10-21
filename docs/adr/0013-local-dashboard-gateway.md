# ADR 0013 — Local authenticated dashboard gateway (step 4.2)

Status: implemented as a **localhost-only first iteration**.

## Problem

Step 4.1 created a static React/TypeScript SPA. The existing
`aegis history` API binds numeric loopback only and requires a
privileged operator Bearer token. Passing that token to Vite / JavaScript
or exposing the API to a reverse proxy would undermine its security
boundary. The browser also needs fleet-wide summary counts without
iterating all pages.

## Decision

Introduce a separate Go command:

```sh
aegis dashboard --listen 127.0.0.1:8082 --static-dir web/dist
```

`AEGIS_DATABASE_URL` identifies PostgreSQL;
`AEGIS_DASHBOARD_TOKEN` is a separate randomly generated 32+ character
password. Do not reuse agent, history or DB credentials. After
`cd web && npm ci && npm run build`, the Go process serves Vite's
compiled HTML/CSS/JS **and** a same-origin read-only JSON API.

The SPA initially checks `GET /api/web/session`. The user enters
a dashboard-specific secret into a password form; the browser sends
it once to `POST /api/web/login`, then discards it. The server
holds SHA-256 digests of authentication material in memory and creates
32 cryptographically random bytes for an opaque session ID.
It stores only the hash of this ID, and issues an `HttpOnly`,
`SameSite=Strict`, path=/ cookie expiring after eight hours.
Sessions are not persistent across process restarts.
`POST /api/web/logout` revokes the cookie's session immediately.
Rate limit: five invalid passwords per minute globally on this
single-process local gateway; concurrent session cap 128.

Endpoints:
- GET `/api/web/session` -> `{authenticated:boolean}`
- POST `/api/web/login` -> session cookie on success
- POST `/api/web/logout` -> session deleted
- GET `/api/web/agents?limit=30&offline_after=1m&cursor=UUID`
  -> existing `storage.AgentResult`
- GET `/api/web/summary?offline_after=1m`
  -> `{total,online,offline,unknown,checked_at,offline_after}`

The API cannot be used without a valid cookie; status counts are
calculated in PostgreSQL in a single query using one statement
timestamp. The list is ordered by UUID with keyset pagination.
The frontend supports page back/forward, threshold selection,
manual/30s updates, loading, empty/unknown/error states, and
session expiry. The server's `last_contact_at` is not a heartbeat.

## Boundaries and risk

Both the socket binding and request Host must be numeric loopback;
not `0.0.0.0`, public DNS or a non-loopback IP. Mutation routes require
matching Origin, rejected cross-site fetch metadata, bounded request
body, SameSite cookies and strict content security headers.
No CORS wildcard. No browser storage of login secret or history
operator token. The Go process queries PostgreSQL directly.
A production deployment MUST use its own PostgreSQL read-only role;
`Ready` needs read access to the schema ledger. This is NOT a
multi-user identity, OAuth/OIDC, or public dashboard.

HTTP and non-Secure cookies are supported **only on numeric loopback**
for local development. Never expose or reverse-proxy this server
to the Internet. Remote production requires HTTPS, Secure cookies,
a separate authenticated operator identity/RBAC system, CSRF defense
and rate-limiting review. Internal-only access does not remove the
need to protect OS access, environment variables and the DB.

Static content is public, but telemetry JSON requires a session.
`aegis dashboard` does not proxy `aegis history`. A completed Vite
bundle is required; Go binary archives do NOT currently bundle
the React assets.

## Testing and limits

Go httptest covers unauthenticated reads, login/logout, strict
Origin/Host, rate limiting, malformed query parameters, DB
error redaction and static SPA fallback. PostgreSQL integration tests
cover fleet-state counts. GitHub Actions builds both Go and TypeScript.
Automated end-to-end browser tests and full production security
hardening are still future work (step 4.6).

Raw telemetry graphs and history on the detail page are intentionally
not connected until step 4.3. Network speed is not estimated from
cumulative bytes.
