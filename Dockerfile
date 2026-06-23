# ---------- Stage 1: build the Go binary ----------
FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Pure-Go sqlite (modernc) → CGO not needed.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/scraper ./cmd/server

# ---------- Stage 2: runtime with Python + Camoufox ----------
FROM python:3.11-slim-bookworm

# Firefox/Camoufox runtime libraries.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        libgtk-3-0 libx11-xcb1 libxcb1 libxcomposite1 libxcursor1 libxdamage1 \
        libxext6 libxfixes3 libxi6 libxrandr2 libxrender1 libxtst6 \
        libasound2 libdbus-glib-1-2 libgbm1 libpango-1.0-0 libcairo2 \
        libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libgdk-pixbuf-2.0-0 \
        fonts-liberation \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Python deps + Camoufox Firefox binary (downloaded at build time so the
# container has no network dependency for the browser at runtime).
COPY scripts/requirements.txt /app/scripts/requirements.txt
RUN pip install --no-cache-dir -r /app/scripts/requirements.txt \
    && python -m camoufox fetch

COPY --from=builder /bin/scraper /app/scraper
COPY scripts /app/scripts
COPY configs /app/configs

# data/ holds the sqlite db, cookies.json, error log — mount as a volume.
RUN mkdir -p /app/data

ENV PYTHON_BIN=python3 \
    FETCH_SCRIPT_PATH=scripts/fetch_page.py \
    DETAIL_SCRIPT_PATH=scripts/fetch_details.py

EXPOSE 8080
CMD ["/app/scraper"]
