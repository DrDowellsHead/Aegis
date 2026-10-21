# ADR 0017 — HTTPS dashboard, operator identities and roles (step 4.6)

Status: initial implementation in `develop`; deployment requires a real certificate and operational review.

## Background

Steps 4.2–4.5 introduced a localhost-only, single-token, HTTP dashboard.
Opening that listener to public IP addresses would expose the session cookie,
credentials and telemetry. The new gateway must not trust forwarding headers
or share the privileged history API bearer token with the browser.

## Design

Retain the legacy local development mode unchanged:

```sh
AEGIS_DASHBOARD_TOKEN=... aegis dashboard --listen 127.0.0.1:8082 --static-dir web/dist
```

A separate explicit mode is available:

```sh
aegis dashboard --remote --listen 0.0.0.0:8443 \
  --public-origin https://monitor.example.com:8443 \
  --users-file /etc/aegis/dashboard-users.json \
  --tls-cert /etc/letsencrypt/live/monitor.example.com/fullchain.pem \
  --tls-key /etc/letsencrypt/live/monitor.example.com/privkey.pem \
  --static-dir web/dist
```

Replace the example hostname. The public origin must match the URL typed in
the browser, including a nondefault port. The leaf certificate must cover the
hostname and be within its validity dates. TLS 1.3 is mandatory; the TLS
connection terminates **in Go**, not at an untrusted HTTP reverse proxy.
The server rejects cleartext HTTP requests, Host mismatches and non-matching
Origin on every POST. Do not publish the older local listener externally.

Accounts are created **offline** through `aegis dashboard-user` with a
password supplied via stdin (never a command-line argument). The private JSON
registry holds bcrypt hashes, names and roles. On Unix, only restrictive file
permissions such as 0600 are accepted. There must always be one administrator.
The registry is read once on dashboard startup. Restart after any change:
previous sessions are in-memory and become invalid. Securely restrict access
to the file and its parent directories; Windows operators must set file ACLs
manually.

There are two roles:
- `viewer`: read agent listing, current state, history and series.
- `admin`: same reads plus list account names/roles and revoke live sessions.

No browser endpoint creates users or changes their passwords. There is no
tenant isolation: both roles can see telemetry from **all** registered agents.
This is an operational dashboard, not a SaaS multi-tenant gateway.

Remote login accepts a username and password over HTTPS. Bcrypt compares
against stored hashes (a dummy hash is checked for unknown usernames).
Throttling is bounded in-memory: 8 attempts per username/minute,
20 per IP/minute and 120 globally/minute. Client-provided forwarded IP
headers are ignored. Repeated failure results in HTTP 429. These counters
are per process, not shared between multiple servers.

Successful login issues a random opaque 256-bit session ID. Only its SHA-256
digest is held server-side; expiration is fixed at eight hours with a maximum
of 128 concurrent sessions. Cookie: `HttpOnly; Secure; SameSite=Strict; Path=/`.
The cookie is cleared at logout; administrators can revoke all sessions for a
specific account. Sessions are lost at server restart.

Strict same-origin POST, Fetch Metadata and SameSite cookies together provide
CSRF protection for the current JSON mutating endpoints. Future mutations must
also enforce the same policy. HSTS is set for the remote HTTPS origin; avoid
including subdomains automatically. CSP forbids inline scripts and framing.
API data is marked `Cache-Control: no-store`.

## Operational limitations

- Real certificate provisioning, DNS, firewall, PostgreSQL read-only credentials,
  backups, certificate renewals, service supervision and monitoring are operator
  responsibilities. Self-signed demonstration certs are not a public production PKI.
- No browser-side storage of passwords or database credentials.
- Direct TLS means an HTTP-terminating CDN/proxy is **not** currently supported;
  a TCP pass-through or direct connection may work, subject to networking.
- User management is offline and deliberately requires a restart; the browser can
  only inspect names/roles and revoke sessions.
- No recovery from losing the last administrator password except offline
  replacement with authenticated host OS access.
- Remote mode depends on one in-memory process. It does not support horizontal
  scaling, SSO/OIDC, MFA, audit database for session events or permission-per-agent.
- Administrator operations are logged using `slog` but logs must be protected
  and centralized by the operator; this is not tamper-proof audit storage.
- A complete public deployment must also consider DDoS mitigation, patching,
  database permissions, OS security, testing against the real PostgreSQL, and
  regular external security review.

## Acceptance checks

- Local HTTP mode remains loopback-only and its tests continue to pass.
- Remote mode refuses an invalid certificate or missing account file.
- Plain HTTP, wrong Host/Origin and unauthenticated APIs are denied.
- Viewers can read metrics but not administrator endpoints.
- Admin can revoke sessions; the revoked cookie cannot read telemetry.
- Restart invalidates all sessions; no password hashes appear in API JSON.
- Go race/tests, TypeScript build and Playwright mocked API E2E must pass.
