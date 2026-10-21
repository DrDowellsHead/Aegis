# ADR 0026 — TLS lifecycle and CA trust rollover (6.2)

Status: implemented in develop; requires successful full CI before release.

## Intent
Agents authenticate the server using a trusted CA and SAN, without disabling TLS verification. A server certificate must be valid, matched to its key, usable for server authentication and (when configured) chain to the explicitly trusted CA.

## Commands
- `aegis tls check --tls-cert server.pem --tls-key server.key --host monitor.example.test --ca-cert ca.pem` prints only public metadata. Missing `--ca-cert` means `chain_verified=false`. Expiry within `--warn-before` (default 720h) causes nonzero exit.
- `aegis tls bundle --current-ca old-ca.pem --next-ca new-ca.pem --output overlap.pem` creates a distinct, new output file. Only CA certificates are accepted; unexpected PEM blocks, private keys and stray text are rejected.

## Rollover
1. Independently authenticate the new CA fingerprint, not merely its filename.
2. Distribute overlap bundle (old + new CA) to all agents; restart agents and verify old server connectivity.
3. Validate new server key, SAN, validity and complete issuer chain. Replace server certificate; restart receiver and verify heartbeat.
4. Once all agents accept new CA, distribute new-only trust, restart agents and verify old CA is no longer trusted.
5. Retire old keys securely. Never set `InsecureSkipVerify`.

## Failure boundaries
Development CA signing keys are discarded: `aegis cert` creates a new CA. No hot reload, automated certificate renewal, CRL/OCSP validation or automatic service orchestration is implemented by step 6.2. Go server checks perform preflight; trust is still independently enforced by TLS clients. A compromised CA bundle breaks the trust model, so distribute it via authenticated channels.

Tests cover mismatch, expiry warning, purpose, SAN, PEM rejection and dual-CA rollover. Production deployment still requires supervised receiver and live agent verification.
