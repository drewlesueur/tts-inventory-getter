# Inventory Scraper Service (Go)

Production-oriented scraper service for dealership inventory extraction.

## Stack
- Go 1.22+
- net/http API
- `chromedp` rendering with HTTP fallback parser
- SQLite for run result persistence
- Dockerized for Linux

## Architecture
- `cmd/server/main.go`: service bootstrap, graceful shutdown, daily scrape cron
- `internal/api`: HTTP handlers/middleware
- `internal/scrape`: browser render, parsing strategies, normalization, detail image fetch, retry, dedupe
- `internal/discovery`: Codex/OpenAI-driven selector discovery
- `internal/inventoryapi`: client for upstream inventory API (scheduled URL list + inventory upserts)
- `internal/config`: env + site config loader (with in-memory cache for discovered configs)
- `internal/store`: SQLite scrape result persistence
- `internal/auth`: API key and optional HMAC middleware
- `internal/metrics`: Prometheus metrics
- `configs/sites/*.yaml`: per-dealership scrape configs

## API
### `POST /v1/scrape/once`
Successful results are cached by dealership and normalized source URL for 24 hours. Requests during that window return the completed cached result without starting another live scrape. The client-provided `idempotencyKey` does not change the cache identity.

Request:
```json
{
  "dealershipId": "txtcharlie",
  "sourceUrl": "https://www.txtcharlie.com/inventory/",
  "idempotencyKey": "run-2026-04-30",
  "options": {
    "runTimeoutSec": 240,
    "browserStrategy": "playwright_first",
    "enableAIEnrichment": true
  }
}
```

Additional scrape options:
- `browserStrategy`: `playwright_first` (default) or `rod_first`
- `enableAIEnrichment`: when true and `OPENAI_API_KEY` is set, fills missing vehicle fields via structured OpenAI output

Per-item response now includes additive aliases for UI mapping:
- `dealerId`, `website`, `vehicleListPrice`, `photoURLs`, `stock`, `style`
- Existing fields (`stockId`, `price`, `images`, `primaryImage`, etc.) are unchanged

### `GET /v1/results/:resultId`
Returns persisted scrape result (status, items, errors) from SQLite.

### `DELETE /v1/results`
Clears every row in the `scrape_results` table and all cached products, including the 24-hour scrape-once cache. Requires `X-Service-Key`. The next scrape-once request performs a live scrape.

### `POST /v1/scrape/daily-upsert`
Manual trigger for the same full inventory upsert pipeline used by the daily cron. Requires `X-Service-Key`.

Response:
```json
{ "status": "accepted", "job": "daily-upsert", "jobId": "..." }
```

`POST /v1/manual-load/daily-upsert` and `POST /v1/cron/daily-upsert` are also supported as aliases.

### `GET /healthz`
Liveness.

### `POST /v1/scrape/discover-flow`
When Codex discovery mode is enabled, fetches/rendered page HTML and proposes extraction config.

Request:
```json
{
  "dealershipId": "txtcharlie",
  "sourceUrl": "https://www.txtcharlie.com/inventory/"
}
```

Response:
```json
{
  "status": "ok",
  "proposedConfig": {
    "name": "txtcharlie",
    "baseUrl": "https://www.txtcharlie.com/inventory/",
    "listPage": {
      "cardSelector": ".vehicle-card",
      "titleSelector": "h2",
      "urlSelector": "a[href*='inventory']",
      "stockSelector": ".stock",
      "priceSelector": ".price",
      "mileageSelector": ".mileage",
      "imageSelector": "img"
    },
    "detailPage": { "imageSelectors": [".gallery img"] },
    "regex": { "stock": ["(?i)stock..."], "vin": ["\\b([A-HJ-NPR-Z0-9]{17})\\b"] }
  }
}
```

## Error Model
All API errors return:
```json
{ "status":"error", "error": { "code":"...", "message":"..." } }
```

## Security
- Required `X-Service-Key`
- Optional HMAC (`X-Request-Timestamp`, `X-Signature`)
- Request body size limit
- Per-IP rate limiting

## Scheduled Scrape Flow
Three independent crons, each toggled by its own `ENABLE_*` flag:

**1. Daily inventory upsert** (`ENABLE_DAILY_UPSERT_CRON`, `DAILY_UPSERT_CRON_SPEC`, default `@daily`)
1. `GET <INVENTORY_API_BASE_URL>/getScrapePageURLList` -> expects `{ "data": { "items": [{accountID, dealershipId, url, ftp_sync, scrape_sync}, ...] } }` (legacy `data` arrays are also accepted).
2. Processes daily entries using `scrapeFrequencyMinutes` when provided, falling back to `schedule.type` (entries without either are treated as daily for compatibility).
3. If an eligible entry has `ftp_sync: true`, calls `POST <INVENTORY_API_BASE_URL>/syncAccountInventorySources` with `{ "accountID": "..." }`.
4. If an eligible entry has `scrape_sync: true`, loads its URL-keyed site config (or pulls from in-memory cache populated by prior discovery). Missing configs are skipped with a warning.
5. Runs `ScrapeOnce` against `url`.
6. For scraped items with a `stockId`, `POST <INVENTORY_API_BASE_URL>/upsertAccountInventory` with full item fields, including detail-page images.

If `scrape_sync` is omitted by an older upstream API response, the scheduler keeps the previous behavior and scrapes the entry. `ftp_sync` must be explicitly true to run FTP/source sync.

Example upsert request:
```bash
curl -X POST 'http://localhost:PORT/upsertAccountInventory' \
  -H 'Content-Type: application/json' \
  -H 'X-Service-Key: replace-with-strong-key' \
  -d '{"accountID":"SALESPERSON_ID","dealershipId":"DEALERSHIP_ID","items":[{"stockId":"STK-1001","vin":"1HGCM82633A123456","year":"2024","make":"Honda","model":"Accord","style":"Sedan","price":"28995","mileage":"12500","color":"Black","primaryImage":"https://example.com/images/accord-1.jpg","images":["https://example.com/images/accord-1.jpg","https://example.com/images/accord-2.jpg"],"website":"https://example.com/inventory/accord"}]}'
```

**2. Weekly inventory upsert** (`ENABLE_WEEKLY_UPSERT_CRON`, `WEEKLY_UPSERT_CRON_SPEC`, default Sunday at 02:00)
Uses the same full inventory upsert pipeline for weekly entries (`scrapeFrequencyMinutes: 10080`, or `schedule.type: weekly` when frequency is absent).

**3. Idempotency clear** (`ENABLE_IDEMPOTENCY_CLEAR_CRON`, `IDEMPOTENCY_CLEAR_CRON_SPEC`, default `@daily`)
Wipes the idempotency-key -> result mapping so old keys can be reused. Result rows themselves are kept (still queryable by `resultId`).

Each per-dealership scrape is also persisted to SQLite for inspection via `GET /v1/results/:resultId`.

## Config
Copy `.env.example` to `.env`.

Key vars:
- `SERVICE_KEY`
- `SQLITE_PATH` (default `data/scraper_results.db`)
- `ENABLE_HMAC`, `HMAC_SECRET`
- `DEFAULT_RUN_TIMEOUT_SEC`, `SCRAPE_CONCURRENCY`
- `ENABLE_DAILY_UPSERT_CRON` + `DAILY_UPSERT_CRON_SPEC` (default `@daily`) - full inventory upsert for daily sources
- `ENABLE_WEEKLY_UPSERT_CRON` + `WEEKLY_UPSERT_CRON_SPEC` (default Sunday at 02:00) - full inventory upsert for weekly sources
- `ENABLE_IDEMPOTENCY_CLEAR_CRON` + `IDEMPOTENCY_CLEAR_CRON_SPEC` (default `@daily`) - clears idempotency-key -> result mapping
- `INVENTORY_API_BASE_URL` (default `http://localhost`)
- `ERROR_LOG_PATH` (default `data/errors.log`) — error-level (and above) log entries are appended here in JSON
- `ENABLE_CODEX_DISCOVERY`, `OPENAI_API_KEY`, `OPENAI_MODEL`

## Codex Discovery Mode
Set these in `.env`:
```env
ENABLE_CODEX_DISCOVERY=true
OPENAI_API_KEY=sk-...
OPENAI_MODEL=gpt-4.1-mini
```

Then call:
```bash
curl -X POST http://localhost:8080/v1/scrape/discover-flow \
  -H "Content-Type: application/json" \
  -H "X-Service-Key: replace-with-strong-key" \
  -d '{"dealershipId":"txtcharlie","sourceUrl":"https://www.txtcharlie.com/inventory/"}'
```

Discovered configs are cached in-process memory (not written to YAML); they are lost on restart and re-discovered on the next request.

## Site Adapters
Add new YAML under `configs/sites/<dealer>.yaml`:
- list selectors (`cardSelector`, title/url/price etc.)
- detail page image selectors
- regex fallback patterns
- pagination/infinite-scroll hints

## Run
### Local
```bash
go mod tidy
go test ./...
go run ./cmd/server
```

### Generic Any-Site Scrape (Rod + Goquery + OpenAI Structured Output)
This repo now includes `cmd/scrapeany`, a generic command to scrape almost any website with:
- Browser render: `rod` (headless Chromium)
- DOM candidate extraction: `goquery`
- AI normalization: OpenAI Responses API with `json_schema` output

Install dependency once:
```bash
go get github.com/go-rod/rod@latest
```

Run:
```bash
OPENAI_API_KEY=sk-... \
OPENAI_MODEL=gpt-5 \
go run ./cmd/scrapeany -url "https://example.com"
```

Optional flags:
- `-timeout 60s`
- `-max-candidates 200`

### Docker
```bash
docker compose up --build
```
# tts-inventory-getter
