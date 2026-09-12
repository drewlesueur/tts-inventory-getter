#!/usr/bin/env bash
set -euo pipefail

CHROME_BIN="${CHROME_BIN:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
CHROME_PROFILE_DIR="${CHROME_PROFILE_DIR:-$HOME/Library/Application Support/SNB-Scraper-Chrome}"
CDP_PORT="${CDP_PORT:-9222}"

if [[ ! -x "$CHROME_BIN" ]]; then
  echo "Chrome not found at $CHROME_BIN; set CHROME_BIN to its executable." >&2
  exit 1
fi

echo "Starting Chrome with CDP on 127.0.0.1:$CDP_PORT. Leave Chrome open while syncing."
exec "$CHROME_BIN" \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port="$CDP_PORT" \
  --user-data-dir="$CHROME_PROFILE_DIR" \
  "https://www.snbmotors.com/cars-for-sale?SoldStatus=AvailableVehicles"
