#!/usr/bin/env python3
"""
upsert_to_taptosign.py — Directly POST scraped items to TapToSign's upsert API.
Run this yourself to test the TapToSign upsert in isolation.

Usage:
  python3 scripts/upsert_to_taptosign.py <accountID> <dealershipId> [scrape_json_file]

If scrape_json_file is omitted, the most recent scrape-*.json in the repo root is used.

Env:
  TAPTOSIGN_URL   default https://taptosign.com
  SERVICE_KEY     X-Service-Key (default replace-with-strong-key)
"""
import os
import sys
import glob
import json
import urllib.request
import urllib.error

TAPTOSIGN_URL = os.environ.get("TAPTOSIGN_URL", "https://taptosign.com").rstrip("/")
SERVICE_KEY = os.environ.get("SERVICE_KEY", "replace-with-strong-key")

if len(sys.argv) < 3:
    print("usage: upsert_to_taptosign.py <accountID> <dealershipId> [scrape_json_file]")
    sys.exit(1)

account_id = sys.argv[1]
dealership_id = sys.argv[2]

if len(sys.argv) > 3:
    src = sys.argv[3]
else:
    files = sorted(glob.glob("scrape-*.json"), key=os.path.getmtime, reverse=True)
    if not files:
        print("no scrape-*.json found in repo root")
        sys.exit(1)
    src = files[0]

data = json.load(open(src))
items = [it for it in data.get("items", []) if it.get("stockId") or it.get("stock")]
print(f"source: {src}")
print(f"upserting {len(items)} items → {TAPTOSIGN_URL}/upsertAccountInventory")
print(f"  accountID={account_id}  dealershipId={dealership_id}")

payload = {"accountID": account_id, "dealershipId": dealership_id, "items": items}
req = urllib.request.Request(
    f"{TAPTOSIGN_URL}/upsertAccountInventory",
    data=json.dumps(payload).encode(),
    headers={"Content-Type": "application/json", "X-Service-Key": SERVICE_KEY},
    method="POST",
)
try:
    resp = urllib.request.urlopen(req, timeout=60)
    print("status:", resp.status)
    print("body:", resp.read().decode()[:1000])
except urllib.error.HTTPError as e:
    print("HTTP", e.code)
    print(e.read().decode()[:1000])
except Exception as e:
    print("error:", e)
