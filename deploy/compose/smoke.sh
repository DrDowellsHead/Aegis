#!/usr/bin/env bash
# CI only: spin up actual PostgreSQL + Go TLS/gRPC receiver and send a sample.
set -euo pipefail
cd "$(dirname "$0")"

export AEGIS_IMAGE_TAG=ci
compose=(docker compose --env-file .env.example -f compose.yaml)
sudo bash ./init-private.sh

cleanup() {
  rc=$?
  if (( rc != 0 )); then
    "${compose[@]}" ps --all || true
    "${compose[@]}" logs --tail=100 postgres migrate receiver watch || true
  fi
  "${compose[@]}" down --remove-orphans >/dev/null 2>&1 || true
  exit "$rc"
}
trap cleanup EXIT

docker run --rm --user 10001:10001 \
  --mount "type=bind,source=$PWD/private/receiver,target=/secrets" \
  aegis-server:ci cert --host receiver \
  --ca-cert /secrets/ca.pem --tls-cert /secrets/tls.crt --tls-key /secrets/tls.key

docker run --rm --user 10001:10001 \
  --mount "type=bind,source=$PWD/private/receiver,target=/secrets" \
  aegis-server:ci provision \
  --agents-file /secrets/agents.json --credentials /secrets/agent-test.json

"${compose[@]}" up -d --no-build postgres migrate receiver watch

success=0
for i in $(seq 1 15); do
  if "${compose[@]}" run --rm --no-deps receiver send \
      --server receiver:9090 \
      --credentials /run/aegis/receiver/agent-test.json \
      --ca-cert /run/aegis/receiver/ca.pem \
      --disk / > /tmp/aegis-compose-smoke.json 2>/tmp/aegis-compose-smoke.err; then
    success=1
    break
  fi
  sleep 2
done
if (( success != 1 )); then
  echo "TLS/gRPC telemetry could not be stored after Compose startup" >&2
  exit 1
fi

python3 - <<'PY'
import json
from pathlib import Path
result = json.loads(Path("/tmp/aegis-compose-smoke.json").read_text())
assert result.get("durable") is True and result.get("received") is True
assert len(result.get("measurement_id", "")) == 32
PY

count=$("${compose[@]}" exec -T postgres psql -U aegis -d aegis -Atc \
  'SELECT count(*) FROM aegis_measurements;')
if [[ "$count" != "1" ]]; then
  echo "Expected one durable PostgreSQL row, got: $count" >&2
  exit 1
fi

for service in receiver watch postgres; do
  if ! "${compose[@]}" ps --status running --services | grep -qx "$service"; then
    echo "Service $service is not running" >&2
    exit 1
  fi
done

echo "Compose integration passed: TLS-authenticated send -> 1 durable PostgreSQL row"
