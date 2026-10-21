# ADR 0030 — PostgreSQL backup and safe empty-target restore (Phase 6.6)

Status: implemented in `develop`; automated integration and real host validation are separate gates.

## Scope and limitations

Aegis persists its measurements, projections, registry, incidents, audit
and notification outbox in PostgreSQL. The `aegis backup` and `aegis restore`
commands use PostgreSQL 17-compatible `pg_dump` and `pg_restore` executables
from the server operator's PATH. They are not bundled into cross-platform
agent releases. The `AEGIS_DATABASE_URL` is the source for backup;
`AEGIS_RESTORE_DATABASE_URL` is the dedicated empty restore target.
Connection parameters are translated to PG* environment variables.
Passwords are not passed on the command line.

These operations **do not** archive external server secrets, private CA/TLS
keys, local agent credentials, dashboard account JSON, local spool queues,
or deployment configuration. Back them up securely and separately.

## Backup

1. Stop destructive retention and migrations during the dump, and ensure
   PostgreSQL has enough disk capacity. `pg_dump` uses a consistent database
   snapshot while writes continue, but separately stored files do not share
   that snapshot.
2. Create a private output directory (`0700` on Unix) and run
   `aegis backup --output backups/YYYYMMDD-HHMM.dump`.
3. The command checks the migration state, runs `pg_dump --format=custom
   --no-owner --no-privileges`, checks archive readability via
   `pg_restore --list`, fsyncs a private temporary file and publishes
   it using a same-directory hard link that cannot overwrite an existing
   destination. A matching `.sha256` sidecar is written with mode `0600`.
4. The pair of files is useful only together. A checksum detects accidental
   corruption, but is **not** an authenticity signature or encryption.
   Encrypt and store offsite copies under restricted permissions. If power
   fails between archive and checksum publication, an incomplete orphan
   can remain and must be inspected manually.

## Restore

1. Obtain a trusted backup and its matching `.sha256` sidecar.
2. Create a **separate empty database** on a PostgreSQL server with compatible
   extensions and roles; never point restore at the production Aegis DB.
3. Set `AEGIS_RESTORE_DATABASE_URL` to this target and run
   `aegis restore --input backups/YYYYMMDD-HHMM.dump --confirm-empty-target`.
4. Before changing the target, the command verifies file permissions,
   SHA-256 and pg_restore archive metadata. The target is checked for
   non-system objects. A populated target is rejected; there is no DROP,
   TRUNCATE or --clean.
5. The restore uses `pg_restore --single-transaction --exit-on-error
   --no-owner --no-privileges`. On failure it rolls back. On success,
   embedded Aegis migrations are applied and `Ready` verifies all required
   migration versions. Restart Aegis processes using the restored database
   only after validation.

Do not use this process to merge two databases or restore on top of
production. Storage roles must have privileges to create/alter objects in the
target. Database identifiers/passwords are not printed by the CLI.

## Automated and physical verification

CI creates a real PostgreSQL 17 backup with known agent data, deliberately
corrupts a copy and checks the restore refuses it, restores the valid archive
into a separately created database, checks the expected record and ensures
a repeated restore into a populated target fails. This tests pg_dump,
pg_restore, checksum enforcement, schema readiness and idempotent migration
execution. CI is not a disaster-recovery rehearsal for production-sized
data or a test of offsite retention and host crashes. Those exercises remain
required before a stable release.
