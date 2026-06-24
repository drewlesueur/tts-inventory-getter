#!/usr/bin/env bash
#
# scrape_and_upsert.sh — Scrape a URL locally (residential IP) and upsert the
# result DIRECTLY to TapToSign. No cloud involved.
#
# Usage:
#   ./scripts/scrape_and_upsert.sh <url> <accountID> <dealershipId>
#
# Example:
#   ./scripts/scrape_and_upsert.sh \
#     "https://www.saiautosale.com/cars-for-sale" \
#     1777493783478065649 sai_auto_101__37601
#
set -euo pipefail

# ── Config (edit/override via env) ────────────────────────────────────────────
TAPTOSIGN_URL="${TAPTOSIGN_URL:-https://taptosign.com}"
SERVICE_KEY="${SERVICE_KEY:-replace-with-strong-key}"
PYTHON_BIN="${PYTHON_BIN:-python3.11}"
TIMEOUT_SEC="${TIMEOUT_SEC:-300}"
LOCAL_PORT="8080"
# ──────────────────────────────────────────────────────────────────────────────

if [ "$#" -lt 3 ]; then
  echo "usage: $0 <url> <accountID> <dealershipId>"
  exit 1
fi
URL="$1"; ACCOUNT_ID="$2"; DEALERSHIP_ID="$3"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
LOCAL_URL="http://localhost:${LOCAL_PORT}"

started_server=""
cleanup() { [ -n "$started_server" ] && kill "$started_server" 2>/dev/null || true; }
trap cleanup EXIT

# 1. Ensure the local scraper is up.
if curl -s -m 3 "${LOCAL_URL}/healthz" >/dev/null 2>&1; then
  echo "[scrape] local scraper already running"
else
  echo "[scrape] starting local scraper..."
  go run ./cmd/server > /tmp/scrape-upsert.log 2>&1 &
  started_server=$!
  for i in $(seq 1 20); do
    sleep 1
    curl -s -m 3 "${LOCAL_URL}/healthz" >/dev/null 2>&1 && { echo "[scrape] up"; break; }
    [ "$i" = "20" ] && { echo "[scrape] ERROR: scraper did not start"; tail -20 /tmp/scrape-upsert.log; exit 1; }
  done
fi

# 2. Scrape locally and save a JSON log (scrape-<host>_<ts>.json in repo root).
echo "[scrape] scraping $URL ..."
"$PYTHON_BIN" - "$URL" "$LOCAL_URL" "$SERVICE_KEY" "$TIMEOUT_SEC" <<'PY'
import sys, json, os, datetime, urllib.request, urllib.parse
url, local, key, timeout = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
req = urllib.request.Request(local + "/v1/scrape/run",
    data=json.dumps({"url": url, "timeoutSec": timeout}).encode(),
    headers={"Content-Type": "application/json", "X-Service-Key": key}, method="POST")
res = json.loads(urllib.request.urlopen(req, timeout=timeout + 30).read())
items = res.get("items") or []
ts = datetime.datetime.now().strftime("%Y%m%dT%H%M%S")
host = urllib.parse.urlparse(url).netloc.replace("www.", "")
path = f"scrape-{host}_{ts}.json"
json.dump({"url": url, "scrapedAt": datetime.datetime.now().isoformat(),
           "itemCount": len(items), "errors": res.get("errors") or [], "items": items},
          open(path, "w"), indent=2)
print(f"[scrape] got {len(items)} items -> {path}")
# hand the file path to the next step
open("/tmp/scrape-upsert-latest.txt", "w").write(path)
sys.exit(0 if items else 2)
PY

LATEST=$(cat /tmp/scrape-upsert-latest.txt)

# 3. Upsert DIRECTLY to TapToSign.
echo "[upsert] pushing to ${TAPTOSIGN_URL} ..."
TAPTOSIGN_URL="$TAPTOSIGN_URL" SERVICE_KEY="$SERVICE_KEY" \
  "$PYTHON_BIN" scripts/upsert_to_taptosign.py "$ACCOUNT_ID" "$DEALERSHIP_ID" "$LATEST"

echo "[done]"
