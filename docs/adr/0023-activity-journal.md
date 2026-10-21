# ADR 0023 — Searchable incident and operator activity (step 5.6)

Status: implemented in `develop`. This is a phase-5 activity journal, **not**
a replacement for the separate retention/backfill audit or a security SIEM.

## Existing evidence

Step 3.9 already stores maintenance executions in `aegis_admin_audit`.
Step 5.3 added `aegis_incident_events` for state transitions. Step 5.5
added `aegis_notification_attempts`. We **do not replace or erase**
these authoritative records.

Migration `013_activity_journal.sql` creates a bounded-metadata
`aegis_activity_log` with a monotonically allocated identity ID,
timestamp, source category, fixed action, server-authored actor, optional
agent/incident IDs, reference, short summary, JSONB details and unique
references back to source events and delivery attempts. The migration
backfills already-existing transitions and attempts in timestamp order,
and installs PostgreSQL AFTER INSERT triggers for subsequent events.
The insertion is **in the same transaction** as the source record,
including the incident transition/outbox commit.

The backfilled historical severity may only be reconstructed from
existing source state; it is not a perfect forensic snapshot. Missing
historical operator *intents* cannot be reconstructed retrospectively.

## Operator attribution and atomicity

Existing business methods remain backwards compatible: direct Go callers
record `system`. The authenticated dashboard passes the server-side
username or `local-operator` into new As methods. The browser cannot
supply or forge actor identity.

Audited mutations/checks include:
- Threshold rule creation and deletion (with agent and rule IDs).
- Manual threshold evaluation (even if it detects no new issue).
- Manual connectivity evaluation, with the observed status and threshold.
- Global cooldown/escalation policy changes, with old/new numeric values.
- Manual incident acknowledge/resolve, via the pre-existing incident-event
  row and new trigger.
- Incident open/escalate/recover with source/system actor.
- Email/Telegram/Webhook delivery attempt outcome (channel, attempt number,
  sanitized error code only).
- Notification route reconfiguration by the dedicated worker, storing
  only channel names, not recipients or URLs.

The business mutation and its new audit entry commit atomically. Invalid
server-side actor metadata prevents the corresponding mutation rather
than silently suppressing the journal. Unchanged policy or notification
routes do not produce redundant change records.

Events are **append-only in application logic**. This is not a
cryptographically tamper-proof, independently replicated audit ledger.
A DB superuser can modify/remove entries; a production read-only
operator should have only SELECT on the journal, and the writer role
should not have UPDATE/DELETE. A future phase-6 security review can
enforce tighter database privileges and external immutable retention.

## Read API and UI

`GET /api/web/activity` reuses authenticated Go dashboard sessions.
Both viewer and admin may read, no mutation endpoint exists.
Filtering includes category, action, exact operator, agent/incident UUID,
literal substring of summary, and RFC3339 time range (up to 366 days when
both ends supplied). Pagination uses descending `id` keysets and a
positive integer cursor, max 100 rows per query. All comparisons are
parameterized, with LIKE wildcards escaped. Invalid or duplicated
parameters result in 400; DB errors are server-logged, never leaked.

`/activity` is a separate React route and displays local-time labels,
server-rendered identity, expandable bounded details, and filters.
It uses the existing Secure/HttpOnly session cookie and is subject
to the same HTTPS/Host/Origin security model as other dashboard GETs.

## Explicit exclusions and caveats

This is **not** a global account-login, password-reset, JWT, session
revocation or maintenance execution audit, nor a real-time push feed.
The existing `aegis_admin_audit` remains the authoritative source for
retention and backfill jobs; merging those administrative/security
records is deferred to phase 6. Historical transition backfill does not
invent absent actor actions.

A larger audit population may need retention/partitioning in phase 6.
The activity ID is allocated before commit, so concurrent transactions
can occasionally appear slightly out of wall-clock order; keyset pages
are consistent by ID, not a strict global commit-time order. A journal
entry is evidence of a committed DB action, not proof of successful
delivery to a human: delivery merely records the provider outcome.

No automatic detector scheduler is added here: the 5.1–5.4 evaluation
operations remain manual. Notification delivery runs separately while
the worker is alive. Database migration and scoped permissions are
required before restarting servers.
