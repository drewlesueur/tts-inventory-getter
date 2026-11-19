# Scraper diagnostics — DevTracker #12861

The shared scraper records structured, per-run diagnostic events. Manual asynchronous jobs retain one bounded trail across all retry attempts; progress responses include the evidence collected so far. Stored results expose it in the additive `diagnostics` property through the existing authenticated `/v1/results/{id}` endpoint. `/v1/scrape/run` also returns diagnostics and stores its result and diagnostics identified by `resultId`. Existing result status and inventory processing are not replaced by a guessed failure classification.

Events cover configuration resolution, the chosen rendering strategy, native HTTP/API calls, Python HTTP/browser fallback output, Chrome document responses and navigation errors, listing card counts, page counts and detected totals, extractor counts, detail fetching, validation rejects, enrichment failures, retry waits and completion. Scheduled scrape results retain the same evidence, including cache use and early configuration/cache failures; scheduled import acknowledgement or failure is appended to that result. Cache events state that inventory was reused and carry its stored timestamp. A successful cache import is not proof of a live scrape.

Each observed HTTP status belongs to that fetch/navigation. No observed status means **unknown**, not 200. `blockMarker` is a matching string observed in page content, not proof of a particular security rule. A missing selector is not automatically declared to be bot protection. The diagnostic version comes from Go build information; `unknown` is explicit when the binary lacks revision metadata. Earlier retry evidence and original Python errors survive the final summary. The Go batch wrapper now preserves stderr on its structured-error branch, closing the previous information-loss gap.

Limits and access:

- At most 160 events per run, retaining the first 16 and the latest 144; `droppedEvents` signals omission.
- At most 16 KiB of subprocess stderr is buffered, with incomplete leading lines discarded after truncation. Diagnostic messages are redacted and capped at 3,000 bytes. Titles are capped at 200 bytes; URL query strings, fragments and user credentials are removed. Known credential fields and bearer values are redacted.
- Page evidence consists of byte counts, a SHA-256 hash, a bounded title and observed block markers. Full HTML, screenshots, cookies, headers and inventory payloads are not added to diagnostics. This avoids a new store of sensitive pages and large binary artifacts. These metadata cannot replace a screenshot/HTML inspection when layout evidence is needed.
- Bounds apply per result; retention still follows the existing result table. This change does not add table-wide pruning or a separate artifact store.
- Existing service-key/HMAC access controls protect results. TapToSign displays the same evidence as text through its existing authenticated scrape routes; it does not expose a new public log endpoint.
- No new dependency, background job, parser rewrite, cache bypass, import rule or browser installation was added. Diagnostics are observational; existing fetch ordering, rendering waits, item acceptance and import semantics remain in place.

## Persistence and rollout

This worktree is based on `1d6b4cc` (the companion scrape-history service changes), isolated from other scraper tasks. Integrate with the deployed service branch before release; this worktree is not proof of the current production version.

The service adds `scrape_results.diagnostics_json TEXT NOT NULL DEFAULT 'null'` using the existing SQLite initialization/migration mechanism. Old records remain readable with no diagnostics; old executables ignore the extra column. No historical backfill is possible. No MongoDB migration is involved. The required schema rollout and backup/rollback checklist is tracked separately in [DevTracker #13247](https://devtracker.ai/task/13247), linked to implementation [#12861](https://devtracker.ai/task/12861). Deploy the service and complete schema verification before expecting evidence in the TapToSign UI. Deploying only the UI still supports old result responses; it cannot recreate missing historical diagnostics.

## Validation and limits

All Go packages build using existing installed dependencies. Following the explicit request for focused automated tests, `RUN_LIVE_STRICKLAND=0 go test -race ./internal/scrape ./internal/store ./internal/api ./cmd/server ./internal/sites -count=1 -timeout=120s` passed. These packages use local fixtures; the opt-in live scrape remains disabled. New coverage includes legacy and fresh SQLite databases, repeated schema initialization, diagnostic round trips through result and idempotency lookup, corrupt optional diagnostics, legacy writers, retry recovery/persistence, late-worker attribution, concurrent snapshots, secret redaction, event/message/stderr limits, native HTTP metadata and partial batch failures. Disposable SQLite files are created under test temporary directories; no MongoDB connection is used.

TapToSign's `src/public/tests/inventory-scrape-diagnostics.test.mjs` passed all four cases, including actual DOM rendering in an installed isolated Chromium headless shell. Set `CHROMIUM_BIN` to an installed compatible Chromium executable when running it; no browser dependency is downloaded. The browser case uses synthetic responses, blocks network access with CSP, and checks old responses, error envelopes, both troubleshooting panels, a long run's final evidence and HTML injection resistance. Python scripts also pass syntax parsing, and both worktrees pass whitespace checks.

No production scrape, import, dependency installation, deployment or migration was performed. Real provider fetches, authenticated live TapToSign integration, and production schema rollout remain outside this fixture validation. Passing local checks does not establish the cause of historical Brown & Brown failures.

This is general diagnostic coverage, not a claim that every failure has a provable root cause. For example, without a received response there may be only a DNS/TLS/timeout error, and a remote process killed before writing evidence cannot supply it. The new records should distinguish what was observed, the failing stage, and what remains unknown. They do not retroactively diagnose the Brown & Brown failures already stored.

## Review follow-up

The review closed missing structured error forwarding in TapToSign, cache/preflight error evidence, batch scanner failure cleanup, redaction of batch errors and quoted credential fields, skipped-import evidence, and late-worker retry attribution. Python diagnostic generation is best effort. New diagnostic-only persistence uses a three-second timeout and logs failure without rejecting otherwise successful processing. The settings panel replaces its bounded diagnostic snapshot instead of dropping later evidence after a display cap. Synchronous results include their inventory items so a returned result ID remains consistent with the existing result reader.

The supplied TapToSign `AGENTS.md` permits automated tests when specifically requested. The follow-up explicitly authorized these focused tests. Fixture-based runtime checks listed above passed, including the late-retry correction; they do not authorize or replace production rollout. The schema rollout remains tracked by #13247.
