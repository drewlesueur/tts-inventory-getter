#!/usr/bin/env python3
"""
sync_to_cloud.py — Run on your LOCAL (residential-IP) machine.

Scrapes a URL via the LOCAL scraper service (which can reach protected sites),
then pushes the result to the CLOUD service's URL-keyed inventory cache. It does
not resolve or upsert an account/dealership.

Usage:
  python3 sync_to_cloud.py <scrape_url>

Env:
  LOCAL_URL     local scraper base url   (default http://localhost:8080)
  CLOUD_URL     cloud scraper base url   (required, e.g. https://your-server)
  SERVICE_KEY   X-Service-Key for both   (required)
  TIMEOUT_SEC   scrape timeout           (default 300)
"""
import os
import sys
import json
import datetime
import urllib.parse
import urllib.request

LOCAL_URL = os.environ.get("LOCAL_URL", "http://localhost:8080").rstrip("/")
CLOUD_URL = os.environ.get("CLOUD_URL", "").rstrip("/")
SERVICE_KEY = os.environ.get("SERVICE_KEY", "")
TIMEOUT_SEC = int(os.environ.get("TIMEOUT_SEC", "300"))
LOG_DIR = os.environ.get("LOG_DIR", ".")

if len(sys.argv) < 2:
    print("usage: sync_to_cloud.py <scrape_url>")
    sys.exit(1)
scrape_url = sys.argv[1]

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
local = post(LOCAL_URL, "/v1/scrape/run", {"url": scrape_url, "timeoutSec": TIMEOUT_SEC,
                                           "options": {"forceLive": True}})
items = local.get("items") or []
print(f"[local] got {len(items)} items")

# Write a timestamped JSON log of the scraped items.
ts = datetime.datetime.now().strftime("%Y%m%dT%H%M%S")
host = urllib.parse.urlparse(scrape_url).netloc.replace("www.", "")
os.makedirs(LOG_DIR, exist_ok=True)
log_path = os.path.join(LOG_DIR, f"scrape-{host}_{ts}.json")
with open(log_path, "w") as f:
    json.dump(
        {
            "url": scrape_url,
            "scrapedAt": datetime.datetime.now().isoformat(),
            "itemCount": len(items),
            "errors": local.get("errors") or [],
            "items": items,
        },
        f,
        indent=2,
    )
print(f"[log]  wrote {len(items)} items to {log_path}")

if not items:
    print("[local] no items — aborting (errors:", local.get("errors"), ")")
    sys.exit(1)

# 2. Push to the URL-keyed cloud cache only.
print(f"[cloud] syncing to {CLOUD_URL} ...")
payload = {"url": scrape_url, "items": items, "skipUpsert": True}
result = post(CLOUD_URL, "/v1/scrape/sync", payload)
print(f"[cloud] {json.dumps(result)}")
