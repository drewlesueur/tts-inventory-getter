# SNB Motors Chrome sync

This standalone Go command connects to your own
Chrome, reads SNB Motors listings and vehicle details, saves JSON, then posts
`{url, items, skipUpsert: true}` to `/v1/scrape/sync`. It updates the URL cache,
just like `scripts/sync_to_cloud.py`. The local scraper server is not needed.
No Python, Node, or browser installation is performed by this command.

Every inventory page uses `SoldStatus=AvailableVehicles`, equivalent to selecting
**Available** in the site's status dropdown. The command verifies that selection
on every page and uses the site's filtered total (not a hardcoded vehicle count).
The cloud cache key remains `https://www.snbmotors.com/cars-for-sale`.

From the repository root, start a separate Chrome profile on macOS (leave it running):

```bash
./cmd/snb-sync/start-chrome.sh
```

Chrome requires a non-default user data directory for remote debugging:
https://developer.chrome.com/blog/remote-debugging-port
Keep the port local and use this separate profile only for scraping.

In another terminal, from the repository root:

```bash
CLOUD_URL="http://54.244.74.98:8080" \
SERVICE_KEY="replace-with-strong-key" \
TIMEOUT_SEC=3600 \
CDP_URL="http://127.0.0.1:9222" \
./cmd/snb-sync/sync.sh
```

This reproduces the supplied endpoint/key; use the actual configured service key.
An HTTP endpoint transmits that key unencrypted; HTTPS is preferred.
These values are also the script defaults, so `./cmd/snb-sync/sync.sh` alone
runs the same sync. Both scripts work from any working directory. From inside
`cmd/snb-sync`, use `./start-chrome.sh` and `./sync.sh` instead.

The sync script finds Go on PATH, then falls back to `/usr/local/go/bin/go`.
Set `GO_BIN` to override that path. Chrome defaults to the macOS application;
set `CHROME_BIN` or `CHROME_PROFILE_DIR` to override the executable or dedicated
profile directory. To change the debugging port, set `CDP_PORT` for both scripts
(an explicit `CDP_URL` takes precedence for syncing).
The Go version requirement is specified by the repository's `go.mod`.

The program opens its own tab in that Chrome session and closes only that tab
when finished. Complete any site challenge manually in the scrape tab. Each
navigation waits up to 120 seconds, subject to the overall `TIMEOUT_SEC` deadline.
Increase `TIMEOUT_SEC` for large inventories (for example, 3600).

Progress includes page counts, unique vehicles, detail counts, HTML bytes read,
and a heartbeat every ten seconds while a page is loading. HTML byte counts
measure the DOM returned by Chrome, not total network traffic.

Options:

```bash
./cmd/snb-sync/sync.sh -dry-run
./cmd/snb-sync/sync.sh -cdp http://127.0.0.1:9222 -output snb.json
```

`CDP_URL` also sets the debugging endpoint. Environment values are read from the
shell; `.env` is not loaded automatically.

Missing or inconsistent pagination, repeated vehicles, failed detail pages, or
a mismatch with the site's reported total abort the run before upload. The
program saves collected results with `complete: false` and an error on failure;
it saves the complete inventory before attempting upload. An upload failure
retains that complete local copy. Ctrl+C cancels the run; rerunning starts fresh.
If interruption happens during upload, the cloud may already have received it;
rerunning replaces the cache for the same URL.

Validation:

```bash
/usr/local/go/bin/go test ./cmd/snb-sync
```

Tests cover HTML parsing, pagination guards, cache-only payloads, and failed or
redirected uploads. A live scrape requires Chrome running with CDP and successful
access to SNB Motors; unit tests cannot establish live completeness.
