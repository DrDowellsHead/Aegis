# ADR 0025 — Agent credential rotation and revocation (step 6.1)

Status: implemented in `develop`; requires receiver restart for enforcement.

## Existing security baseline

Step 3.2 introduced a unique random 256-bit agent bearer token, a
version-4 agent UUID, an on-disk client credential file and a server
registry containing only SHA-256 digests of random tokens. gRPC
telemetry and heartbeat authenticate against a registry loaded when
`aegis receive` starts, over server-authenticated TLS. Each token
belongs to exactly one agent UUID, so it must not be copied between
different agents.

Step 6.1 adds these *offline* maintenance commands:

- `aegis agent-token rotate --agents-file <registry> --agent-id <UUID> --credentials <new-file>`
- `aegis agent-token revoke --agents-file <registry> --agent-id <UUID>`

`rotate` verifies that the UUID already exists, generates a new
cryptographically random 256-bit bearer token, writes it exclusively
into a *new* private JSON credential file, and atomically replaces
the server-side registry with a SHA-256 digest of the new token. The
UUID is unchanged, so existing measurement, incident, alert and audit
history remain tied to that agent. The old client file is never
overwritten and the raw new token is **never printed**.

`revoke` removes the specified UUID's digest from the registry. It
does not delete client-side files, PostgreSQL telemetry, alert history
or audit history. On restart, `aegis receive` re-synchronizes the
PostgreSQL registered-agent list, so the revoked agent no longer
participates in normal registered-agent scans.

## Deployment procedure: planned rotation

1. Back up the private server registry to a secure location. Ensure
   only one operator/process modifies it at a time.
2. Generate replacement credentials using `rotate`. The output path
   must be different from the registry path and must not already exist.
3. Transfer the private replacement JSON file to the *same* agent over
   a protected channel. Stop or reconfigure that agent.
4. Restart `aegis receive` to load the updated registry. A running
   receiver retains its old in-memory snapshot, so simply editing
   the JSON file does **not** revoke the previous bearer token.
5. Start the agent using the new file, check accepted heartbeats,
   and verify that the previous token now receives Unauthenticated.
6. Securely retire the obsolete client credential file and any
   backups that contain the old bearer token according to your
   operational retention policy.

Rotation is immediate replacement in the registry, **without a
grace period that accepts both tokens**. A brief maintenance outage
may occur between restarting the receiver and deploying the new
client file. A grace-period/dual-key rotation with expirations is
a separate potential feature and needs a dedicated threat analysis.

## Deployment procedure: revocation

1. Run `agent-token revoke` for the exact UUID in the server registry.
2. Restart `aegis receive` immediately to enforce the new registry.
3. Confirm the agent's old token can no longer authenticate; retire
   the stolen/untrusted client credential and disable the host if
   necessary.
4. Do **not** delete PostgreSQL measurements or associated history:
   they may be needed for incident investigation.

The command is deliberately not exposed through the HTTP dashboard,
preventing a compromised low-privilege browser session from issuing
or downloading reusable bearer tokens.

## Failure and concurrency boundaries

Only private files are written. Unix files are created with 0600
permissions, with symlinks and existing replacement-file paths
rejected. Windows ACL reviews still require OS-specific operational
verification. The new credential file is written first so a write
failure cannot publish an inaccessible new server token. If replacing
the server registry fails, the new file is removed on a best-effort
basis; the error contains no token material.

**Two separate files cannot be committed atomically together.**
A crash between writes may leave a still-unrecognized replacement
file. In that case, inspect the private registry safely and repeat
rotation to a fresh filename if needed; don't assume the first
replacement token became active.

Also, the current JSON registry is a **single-writer configuration**.
It is *not* safe to run parallel provisioning, rotation or revocation
commands against the same file from multiple processes or machines:
the last rename could overwrite another operator's change. Operate
sequentially under a maintenance lock/procedure. Interprocess file
locking, multi-key grace periods and hot reload are **not**
implemented by 6.1.

An attacker with filesystem permission to modify the registry may
replace digests, so production filesystem ACLs and backups remain
security-critical. The SHA-256 hashes are appropriate because tokens
are high-entropy random 256-bit strings, not human passwords. Human
dashboard passwords continue to use bcrypt instead.

## Tests

Cross-platform Go tests assert that:
- old/new bearer tokens differ and the UUID is unchanged;
- the reloaded registry accepts only the new token for that UUID;
- other agents are unaffected by rotation or revocation;
- absent/malformed UUIDs are rejected;
- existing client files or symlink destinations cannot be overwritten;
- token bytes do not enter CLI output or errors; and
- the operator is informed about the mandatory receiver restart.

End-to-end process restart and OS-specific ACL handling require
dedicated integration/host testing. This step alone is not a claim of
production-ready PKI or online token rotation.
