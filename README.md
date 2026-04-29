# Inventory Scraper Service (Go)

Production-oriented scraper service for dealership inventory extraction.

## Stack
- Go 1.22+
- Gin HTTP API
- `chromedp` rendering with HTTP fallback parser
- MongoDB for run logs only
- Dockerized for Linux

## Architecture
- `cmd/server/main.go`: service bootstrap and graceful shutdown
- `internal/api`: HTTP handlers/middleware
- `internal/scrape`: browser render, parsing strategies, normalization, detail image fetch, retry, dedupe
- `internal/discovery`: Codex/OpenAI-driven selector discovery
- `internal/config`: env + site config loader
- `internal/store`: Mongo run summary persistence
- `internal/auth`: API key and optional HMAC middleware
- `internal/metrics`: Prometheus metrics
- `configs/sites/txtcharlie.yaml`: default dealer config

## API
### `POST /v1/scrape/once`
Request:
```json
{
  "dealershipId": "txtcharlie",
  "sourceUrl": "https://www.txtcharlie.com/inventory/",
  "idempotencyKey": "run-2026-04-30",
  "options": { "runTimeoutSec": 240 }
}
```

### `GET /v1/runs/:runId`
Returns persisted run status/summary from Mongo.

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

## Config
Copy `.env.example` to `.env`.

Key vars:
- `SERVICE_KEY`
- `MONGO_URI`
- `ENABLE_HMAC`, `HMAC_SECRET`
- `DEFAULT_RUN_TIMEOUT_SEC`, `SCRAPE_CONCURRENCY`
- `ENABLE_CRON`, `CRON_SPEC`
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

### Docker
```bash
docker compose up --build
```
# tts-inventory-getter
