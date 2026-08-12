#!/usr/bin/env bash
#
# run-sync.sh — Scrape a URL locally (residential IP) and sync the result to the
# cloud cache + dealer. Run this manually on your Mac/home machine.
#
# Usage:
#   ./scripts/run-sync.sh                          # uses DEFAULT_URL below
#   ./scripts/run-sync.sh <url>
#   ./scripts/run-sync.sh <url> <dealershipId> <accountId>
#   ./scripts/run-sync.sh https://www.jjsadobeauto.com/cars-for-sale
#
set -euo pipefail

# ── Config (edit these) ───────────────────────────────────────────────────────
CLOUD_URL="${CLOUD_URL:-http://54.244.74.98:8080}"
SERVICE_KEY="${SERVICE_KEY:-replace-with-strong-key}"
TIMEOUT_SEC="${TIMEOUT_SEC:-300}"
DEFAULT_URL="https://www.saiautosale.com/cars-for-sale"
# Leave dealer/account empty so the cloud resolves the REAL account/dealer that
# owns the URL (from TapToSign's page list). Only pass them to override.
DEFAULT_DEALER=""
DEFAULT_ACCOUNT=""
LOCAL_PORT="8080"
# ──────────────────────────────────────────────────────────────────────────────

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
PYTHON_BIN="${PYTHON_BIN:-$ROOT_DIR/.venv/bin/python}"

URL="${1:-$DEFAULT_URL}"
DEALER="${2:-$DEFAULT_DEALER}"
ACCOUNT="${3:-$DEFAULT_ACCOUNT}"
LOCAL_URL="http://localhost:${LOCAL_PORT}"

started_server=""
cleanup() {
  if [ -n "$started_server" ]; then
    echo "[run-sync] stopping local scraper (pid $started_server)"
    kill "$started_server" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# 1. Make sure the local scraper is running (start it if not).
if curl -s -m 3 "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
  echo "[run-sync] local scraper already running on :${LOCAL_PORT}"
else
  echo "[run-sync] starting local scraper..."
  ./startme.sh > /tmp/run-sync-scraper.log 2>&1 &
  started_server=$!
  for i in $(seq 1 20); do
    sleep 1
    if curl -s -m 3 "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
      echo "[run-sync] local scraper is up"
      break
    fi
    if [ "$i" = "20" ]; then
      echo "[run-sync] ERROR: local scraper did not start (see /tmp/run-sync-scraper.log)"
      tail -20 /tmp/run-sync-scraper.log || true
      exit 1
    fi
  done
fi

if [ ! -x "$PYTHON_BIN" ]; then
  echo "[run-sync] ERROR: local Python environment is unavailable (run ./startme.sh once)"
  exit 1
fi

# 2. Scrape locally + push to the cloud.
echo "[run-sync] url=$URL dealer=$DEALER account=$ACCOUNT cloud=$CLOUD_URL"
CLOUD_URL="$CLOUD_URL" \
LOCAL_URL="$LOCAL_URL" \
SERVICE_KEY="$SERVICE_KEY" \
TIMEOUT_SEC="$TIMEOUT_SEC" \
  "$PYTHON_BIN" scripts/sync_to_cloud.py "$URL" "$DEALER" "$ACCOUNT"

echo "[run-sync] done."
