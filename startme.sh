#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required but not installed. Install Go 1.22+ and retry."
  exit 1
fi

PYTHON_BOOTSTRAP_BIN=""
for candidate in python3.12 python3.11 python3.10 python3; do
  if command -v "$candidate" >/dev/null 2>&1; then
    PYTHON_BOOTSTRAP_BIN="$candidate"
    break
  fi
done

if [ -z "$PYTHON_BOOTSTRAP_BIN" ]; then
  echo "Python 3 is required for DataDome-protected inventory sites."
  exit 1
fi

VENV_DIR="$ROOT_DIR/.venv"
if [ ! -x "$VENV_DIR/bin/python" ]; then
  echo "Creating Python scraper environment..."
  "$PYTHON_BOOTSTRAP_BIN" -m venv "$VENV_DIR"
fi

echo "Checking Python scraper dependencies..."
"$VENV_DIR/bin/python" -m pip install --quiet --disable-pip-version-check -r scripts/requirements.txt

if ! "$VENV_DIR/bin/python" -m camoufox path >/dev/null 2>&1; then
  echo "Downloading the Camoufox browser..."
  "$VENV_DIR/bin/python" -m camoufox fetch
fi

# Environment variables take precedence over a stale PYTHON_BIN value in .env.
export PYTHON_BIN="$VENV_DIR/bin/python"

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
