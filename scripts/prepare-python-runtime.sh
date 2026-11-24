#!/usr/bin/env bash
set -euo pipefail
umask 077

if [[ $# -ne 1 || $1 != /* ]]; then
  printf 'Usage: %s <new absolute venv path>\n' "$0" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
runtime=$1
# mkdir refuses to reuse an existing directory, including a partial environment.
mkdir -- "$runtime"
python3.12 -I -m venv "$runtime"
env -i PATH="$PATH" HOME="$runtime" TMPDIR="$runtime" PIP_CONFIG_FILE=/dev/null \
  "$runtime/bin/python" -I -m pip --disable-pip-version-check --no-cache-dir install \
  --only-binary=:all: --no-deps -r "$script_dir/requirements.txt"
"$runtime/bin/python" -I -m pip check
"$runtime/bin/python" -I -c '
from curl_cffi import requests
from camoufox.async_api import AsyncCamoufox
from playwright.async_api import async_playwright
from browserforge.fingerprints import FingerprintGenerator
import apify_fingerprint_datapoints
'
printf 'Verified Python runtime at %s/bin/python\n' "$runtime"
