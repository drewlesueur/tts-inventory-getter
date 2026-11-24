#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required but not installed. Install Go 1.22+ and retry."
  exit 1
fi

if [ ! -f .env ]; then
  if [ -f .env.example ]; then
    cp .env.example .env
    echo "Created .env from .env.example. Update secrets before production use."
  else
    echo ".env.example not found; create .env manually."
    exit 1
  fi
fi

echo "Downloading dependencies..."
go mod download

echo "Starting scraper service on configured PORT..."
exec go run ./cmd/server
