#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
module="github.com/DrDowellsHead/Aegis"
protoc --proto_path=api/proto \
  --go_out=. --go_opt="module=$module" \
  --go-grpc_out=. --go-grpc_opt="module=$module" \
  api/proto/aegis/telemetry/v1/telemetry.proto
