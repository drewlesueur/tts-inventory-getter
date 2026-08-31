#!/usr/bin/env python3
"""
hybrid_sync.py — Run on your LOCAL (residential-IP / VPN) machine, on a cron.

The cloud server auto-flags bot-protected URLs (DataDome etc.) and serves them
from its synced cache. This worker keeps that cache fresh:

  1. GET  {CLOUD}/v1/scrape/pending-sync   -> URLs whose cache is missing/stale
  2. POST {LOCAL}/v1/scrape/run            -> live-scrape each one locally
  3. POST {CLOUD}/v1/scrape/sync           -> push items to the cloud cache

Usage:
  python3 hybrid_sync.py                # one pass over pending URLs
  python3 hybrid_sync.py --loop 3600    # repeat every N seconds

Env:
  LOCAL_URL        local scraper base url   (default http://localhost:8080)
  CLOUD_URL        cloud scraper base url   (required)
  SERVICE_KEY      X-Service-Key for both   (required)
  TIMEOUT_SEC      per-scrape timeout       (default 1200)
  MAX_AGE_HOURS    cache freshness window   (default 12)
  SYNC_SKIP_UPSERT set to 1 to only cache on the cloud, skipping dealer upsert
"""
import os
import sys
import json
import time
import urllib.request

LOCAL_URL = os.environ.get("LOCAL_URL", "http://localhost:8080").rstrip("/")
CLOUD_URL = os.environ.get("CLOUD_URL", "").rstrip("/")
SERVICE_KEY = os.environ.get("SERVICE_KEY", "")
TIMEOUT_SEC = int(os.environ.get("TIMEOUT_SEC", "1200"))
MAX_AGE_HOURS = int(os.environ.get("MAX_AGE_HOURS", "12"))
SKIP_UPSERT = os.environ.get("SYNC_SKIP_UPSERT", "") in ("1", "true", "yes")

if not CLOUD_URL or not SERVICE_KEY:
    print("CLOUD_URL and SERVICE_KEY env vars are required")
    sys.exit(1)


def call(base, path, payload=None, method=None, timeout=60):
    req = urllib.request.Request(
        f"{base}{path}",
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={"Content-Type": "application/json", "X-Service-Key": SERVICE_KEY},
        method=method or ("POST" if payload is not None else "GET"),
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def one_pass():
    pending = call(CLOUD_URL, f"/v1/scrape/pending-sync?maxAgeHours={MAX_AGE_HOURS}")
    urls = [p["url"] for p in pending.get("pending", [])]
    if not urls:
        print("[hybrid] nothing pending — all caches fresh")
        return
    print(f"[hybrid] {len(urls)} url(s) need sync: {urls}")
    for u in urls:
        try:
            print(f"[hybrid] scraping locally: {u}")
            res = call(LOCAL_URL, "/v1/scrape/run",
                       {"url": u, "timeoutSec": TIMEOUT_SEC, "options": {"forceLive": True}},
                       timeout=TIMEOUT_SEC + 60)
            items = res.get("items") or []
            errors = res.get("errors") or []
            if not items:
                print(f"[hybrid] SKIP {u}: local scrape returned 0 items, errors={[e.get('code') for e in errors]}")
                continue
            # Don't clobber a good cache with a partial scrape (e.g. pagination
            # failed and only page 1 came back).
            try:
                from urllib.parse import quote
                existing = call(CLOUD_URL, f"/v1/scrape/cache?url={quote(u, safe='')}", None)
                prev = existing.get("itemCount") or 0
            except Exception:
                prev = 0
            if prev > 0 and len(items) < prev * 0.6:
                print(f"[hybrid] SKIP {u}: scrape returned {len(items)} but cache has {prev} — looks partial, not overwriting")
                continue
            payload = {"url": u, "items": items}
            if SKIP_UPSERT:
                payload["skipUpsert"] = True
            sync = call(CLOUD_URL, "/v1/scrape/sync", payload, timeout=120)
            print(f"[hybrid] synced {u}: cached={sync.get('cachedItems')} upserted={sync.get('upserted')} dealership={sync.get('dealershipId')}")
        except Exception as e:
            print(f"[hybrid] ERROR {u}: {e}")


def main():
    loop_sec = 0
    if len(sys.argv) > 2 and sys.argv[1] == "--loop":
        loop_sec = int(sys.argv[2])
    while True:
        one_pass()
        if loop_sec <= 0:
            break
        time.sleep(loop_sec)


if __name__ == "__main__":
    main()
