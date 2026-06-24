#!/usr/bin/env python3
"""
sync_to_cloud.py — Run on your LOCAL (residential-IP) machine.

Scrapes a URL via the LOCAL scraper service (which can reach DataDome sites),
then pushes the result to the CLOUD service's /v1/scrape/sync endpoint. The cloud
caches it and upserts to the owning dealer/account.

Usage:
  python3 sync_to_cloud.py <scrape_url> [dealershipId] [accountId]

Env:
  LOCAL_URL     local scraper base url   (default http://localhost:8080)
  CLOUD_URL     cloud scraper base url   (required, e.g. https://your-server)
  SERVICE_KEY   X-Service-Key for both   (required)
  TIMEOUT_SEC   scrape timeout           (default 300)
"""
import os
import sys
import json
import urllib.request

LOCAL_URL = os.environ.get("LOCAL_URL", "http://localhost:8080").rstrip("/")
CLOUD_URL = os.environ.get("CLOUD_URL", "").rstrip("/")
SERVICE_KEY = os.environ.get("SERVICE_KEY", "")
TIMEOUT_SEC = int(os.environ.get("TIMEOUT_SEC", "300"))

if len(sys.argv) < 2:
    print("usage: sync_to_cloud.py <scrape_url> [dealershipId] [accountId]")
    sys.exit(1)
scrape_url = sys.argv[1]
dealership_id = sys.argv[2] if len(sys.argv) > 2 else ""
account_id = sys.argv[3] if len(sys.argv) > 3 else ""

if not CLOUD_URL or not SERVICE_KEY:
    print("CLOUD_URL and SERVICE_KEY env vars are required")
    sys.exit(1)


def post(base, path, payload):
    req = urllib.request.Request(
        f"{base}{path}",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "X-Service-Key": SERVICE_KEY},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=TIMEOUT_SEC + 30) as resp:
        return json.loads(resp.read())


# 1. Scrape locally (residential IP → works against DataDome)
print(f"[local] scraping {scrape_url} ...")
local = post(LOCAL_URL, "/v1/scrape/run", {"url": scrape_url, "timeoutSec": TIMEOUT_SEC})
items = local.get("items") or []
print(f"[local] got {len(items)} items")
if not items:
    print("[local] no items — aborting (errors:", local.get("errors"), ")")
    sys.exit(1)

# 2. Push to the cloud cache + dealer upsert
print(f"[cloud] syncing to {CLOUD_URL} ...")
payload = {"url": scrape_url, "items": items}
if dealership_id:
    payload["dealershipId"] = dealership_id
if account_id:
    payload["accountId"] = account_id
result = post(CLOUD_URL, "/v1/scrape/sync", payload)
print(f"[cloud] {json.dumps(result)}")
