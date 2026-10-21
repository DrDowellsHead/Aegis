#!/usr/bin/env bash
# One-time Linux VPS provisioning. Never overwrites database credentials.
set -euo pipefail
umask 077

if [[ ${EUID} -ne 0 ]]; then
  echo "Run this script as root (sudo); required for numeric ownership." >&2
  exit 1
fi
cd "$(dirname "$0")"
if [[ -e private/db-url || -e private/postgres-password ]]; then
  echo "Refusing to overwrite existing PostgreSQL credentials." >&2
  exit 1
fi
command -v docker >/dev/null
command -v openssl >/dev/null
pg_uid="$(docker run --rm --entrypoint id postgres:17-bookworm -u postgres)"
pg_gid="$(docker run --rm --entrypoint id postgres:17-bookworm -g postgres)"
if ! [[ "$pg_uid" =~ ^[0-9]+$ && "$pg_gid" =~ ^[0-9]+$ ]]; then
  echo "Could not resolve PostgreSQL container numeric UID/GID." >&2
  exit 1
fi

# Allow the nonroot operator to traverse known bind paths without listing
# secret filenames; individual files and private subdirectories remain 0600/0700.
install -d -m 0711 private
for component in receiver dashboard notify; do
  install -d -m 0700 "private/$component"
  chown 10001:10001 "private/$component"
done
password="$(openssl rand -hex 32)"
printf '%s\n' "$password" > private/postgres-password
printf 'postgres://aegis:%s@postgres:5432/aegis?sslmode=disable\n' "$password" > private/db-url
chmod 0600 private/postgres-password private/db-url
chown "$pg_uid:$pg_gid" private/postgres-password
chown 10001:10001 private/db-url
unset password

if [[ ! -e .env ]]; then
  cp .env.example .env
  chmod 0600 .env
fi
echo "Created private DB credentials and empty per-service directories."
echo "Provision TLS certificate/key and agents.json into private/receiver (owner 10001, files 0600)."
echo "Do not start optional dashboard/notify profiles until their files and settings are ready."
echo "Database password was not printed. Store a securely encrypted off-host backup."
