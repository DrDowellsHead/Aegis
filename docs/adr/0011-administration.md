# ADR 0011: Admin configuration, diagnostics and audited maintenance

Status: step 3.9 (operator-only baseline, no public administration API).

## Configuration and secret boundaries

The optional environment variable `AEGIS_ADMIN_CONFIG` points to a
strictly parsed JSON file (version 1, max 64 KiB). Only non-secret defaults
are permitted: retention_days, retention_batch, retention_max_delete and
backfill_batch. Unknown keys, including `database_url`, `password` and
`token`, are rejected. Effective values can be inspected with
`aegis config [--file path]`. CLI flags override configured defaults.
A missing config means the safe built-in defaults; retention days stay
UNSET to require an explicit operator choice.

Database credentials remain in `AEGIS_DATABASE_URL`; the history reader
uses its separate `AEGIS_HISTORY_TOKEN`. This phase does not merge
agent, server and operator settings into a single risky configuration file.
`AEGIS_OPERATOR` is an optional self-declared alphanumeric audit label
(default: local-operator). It is NOT authenticated OS identity or RBAC.

## Diagnostics

`aegis doctor` reads the admin configuration, pings PostgreSQL,
checks the schema ledger and tries reading the audit table.
It returns a machine-readable JSON report with boolean checks and
allowlisted problem codes. Raw PostgreSQL errors and DSNs are never
included in diagnostic output. Failed readiness yields a nonzero exit
code. It does not apply migrations or modify any tables.
`aegis audit --limit 20` reads recent operator actions through an
authenticated DB connection; there is no externally published audit API.

## Audit model and transaction boundaries

Migration 007 adds `aegis_admin_audit` with an operation UUID,
allowlisted action (retention/backfill), self-declared actor, scope,
status, start/end time and affected_rows. Never store the command line,
DSN, tokens, passwords, stack traces or raw SQL in audit rows.

The CLI writes the `running` audit entry BEFORE any mutation. If this
insert fails, it refuses to mutate data (fail closed). During maintenance
each batch opens a PostgreSQL transaction:
- DELETE or projection UPDATE
- UPDATE audit.affected_rows += affected_count
- COMMIT

Both modifications are committed or rolled back together. A missing,
finished or invalid audit record causes rollback. A final status is
written `succeeded`, `failed` or `interrupted` with a short
independent cleanup context. If the process is SIGKILLed or loses the
database, a row may remain `running` with partial committed progress:
that means **unknown completion, never success**.

Schema migration (`aegis migrate`) is explicitly outside this audit:
the audit table may not yet exist or the migration itself may fail. Its
change history is recorded in aegis_schema_migrations. Similarly,
provisioning an agent modifies a private JSON file and is not yet
covered by this database audit; audit expansion requires a deliberate
design for multi-resource transactions. Read-only previews are not
currently audited.

## Security and operation

The audit table is tamper-evident only to the extent database access
controls, backups and external monitoring protect it. It is **not**
cryptographically tamper-proof, not externally replicated and not
an authenticated human identity. Production deployment should use
separate PostgreSQL roles for the read API, ingestion and admin commands;
only the admin role may update audit rows. Audit-table retention,
rotating credentials, centralized logs, signing and operator RBAC
remain future work.

No background tasks, database secret files or public administration
endpoints are introduced in step 3.9.
