# ADR 0027 — Durable dashboard authentication audit and role boundaries (6.3)

Status: implemented in `develop`, pending CI confirmation.

## Existing access-control model

Aegis has two mutually exclusive browser gateway modes:
- **Local**: one operator token over HTTP, bound to numeric loopback only.
- **Remote**: directly terminated HTTPS with exact Host/Origin checks,
  bcrypt password records in a private versioned JSON users file, 8-hour
  HttpOnly/Secure/SameSite=Strict sessions, IP+username+global login
  rate limiting and RBAC (`admin` or `viewer`).

The remote viewer may read all agents/metrics/rules/incidents within
one installation, but cannot mutate incidents/rules/policies or manage
accounts. The admin may manage session revocation and configuration.
**There is no tenant-level or per-agent isolation**: do not place
unrelated customer tenants in the same Aegis instance.

## What 6.3 changes

Migration 015 creates a **separate security audit** table
`aegis_security_events` with server-side timestamp, bounded action,
actor and subject, role, and affected session count. Actions are:

- `login_ok` — verified remote account and issued session.
- `login_failed` — failed known username or unknown account.
- `login_throttled` — rejected rate-limited attempt.
- `logout` — successful remote session logout.
- `sessions_revoked` — admin-directed removal of active sessions.
- `admin_denied` — authenticated viewer attempted admin operations.

The raw supplied login name is **never trusted as audit actor** unless
the account is known: unknown account probes are recorded under
`unknown`. Passwords, hashes, cookie/session tokens, IP addresses,
request bodies, arbitrary URLs and user-agent strings are never stored.

Remote dashboard construction **requires** an audit-capable storage
backend; it is not an optional best-effort logger. Login success writes
the audit entry *before* the cookie is issued; if the audit fails, the
unadvertised session is removed and HTTP 503 is returned. Admin session
revocation and remote logout write their event while synchronizing with
the in-memory session map. If DB audit fails, sessions remain intact
rather than silently completing an unaudited change. Failed/throttled
logins are denied either way; a failed audit becomes 503.

The current architecture cannot provide a fully atomic transaction
spanning PostgreSQL and in-process Go session memory. If a process
crashes after the DB audit write but before the HTTP response, a
`login_ok` or `sessions_revoked` record can describe an action whose
client never received confirmation. Conversely, sessions themselves
are not durable and restart invalidates them. Logs record server-side
processing, **not proof the client obtained a cookie**.

## Access to the audit

`GET /api/web/admin/security-events` is admin-only (read-only) and
accepts `action`, exact `actor`, `limit` (1–100, default 50)
and a positive descending-ID cursor. Unknown/duplicated query params
produce 400; viewers receive 403, anonymous callers 401, and local
mode 404. No mutation endpoint exists.

The remote Settings page displays the last events with filters and
cursor pagination **only for admins**. The existing incident/operator
activity journal remains accessible to viewers; login records are
isolated into this new privileged view.

The security audit is append-only by application contract, **not**
cryptographically immutable. Database administrators remain capable
of modifying its contents unless separately restricted. Production
read-only analytics roles should receive SELECT only; application
writers should not have DELETE/UPDATE privileges on audit history.

## Account-file management boundaries

`aegis dashboard-user` continues to be an **offline filesystem**
operation: bcrypt hash generation, restricted local files, one-admin
minimum and a required remote dashboard restart to apply user changes
and discard all previous sessions. Because it is not executed through
the online dashboard, changing the account JSON file does not create
a PostgreSQL security event in this step. Record such operations in
the host's OS audit/supervisor logs, limit access to the users file and
back it up securely. Future steps may add signed configuration-change
audit with explicit cross-process authorization.

This step does not implement per-agent ACLs, SSO/OIDC, MFA, durable
sessions, login history prior to migration, audit export or tamper-proof
external retention. Cross-instance rate-limit sharing is not present.
These require separate design and operational validation.

## Deployment and tests

Apply `aegis migrate` **before** starting remote dashboard; database
readiness now requires migration 015. Restart the dashboard to load
the new version. Verify that admin can see security history under
Settings, a viewer receives 403 from the audit endpoint, and a login
attempt when the database is unavailable is not accepted.

Unit and PostgreSQL tests cover validation, result pagination, unknown
actions, fail-closed successful login/logout/session revocation, viewer
authorization and redaction of SQL/connection errors. Browser tests
cover Settings visibility per role.
