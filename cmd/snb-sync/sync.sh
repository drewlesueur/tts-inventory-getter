#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export CLOUD_URL="${CLOUD_URL:-http://54.244.74.98:8080}"
export SERVICE_KEY="${SERVICE_KEY:-replace-with-strong-key}"
export TIMEOUT_SEC="${TIMEOUT_SEC:-3600}"
export CDP_URL="${CDP_URL:-http://127.0.0.1:${CDP_PORT:-9222}}"

if [[ -z "${GO_BIN:-}" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  else
    GO_BIN="/usr/local/go/bin/go"
  fi
fi
if [[ ! -x "$GO_BIN" ]]; then
  echo "Go not found; set GO_BIN to the Go executable." >&2
  exit 1
fi

exec "$GO_BIN" run ./cmd/snb-sync "$@"
