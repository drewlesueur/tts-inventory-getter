#!/usr/bin/env python3
"""
sync_cookie.py — Reads the datadome cookie directly from Chrome's local database
and POSTs it to the scraper server. Run this on your local PC.

Usage:
  python3 sync_cookie.py <server_url> <service_key> [site]

Examples:
  python3 sync_cookie.py https://myserver.com my-secret-key
  python3 sync_cookie.py https://myserver.com my-secret-key saiautosale.com
  python3 sync_cookie.py http://localhost:8080 replace-with-strong-key

No DevTools needed — reads directly from Chrome's cookie database.
"""
import sys
import json
import urllib.request
import urllib.error
import urllib.parse

SERVER_URL  = sys.argv[1].rstrip("/") if len(sys.argv) > 1 else "http://localhost:8080"
SERVICE_KEY = sys.argv[2] if len(sys.argv) > 2 else "replace-with-strong-key"
SITE_URL    = f"https://www.{sys.argv[3]}" if len(sys.argv) > 3 else "https://www.saiautosale.com"


def get_cookie_from_chrome(site_url: str) -> str:
    """Read datadome cookie from Chrome's local database (no browser needed)."""
    try:
        from pycookiecheat import chrome_cookies
        cookies = chrome_cookies(site_url)
        value = cookies.get("datadome", "")
        if value:
            print(f"[chrome] ✓ found datadome cookie ({len(value)} chars)")
        else:
            print("[chrome] datadome cookie not found — visit the site in Chrome first")
        return value
    except Exception as e:
        print(f"[chrome] failed to read cookies: {e}")
        return ""


def get_cookie_from_firefox(site_url: str) -> str:
    """Fallback: read datadome cookie from Firefox's database."""
    try:
        import sqlite3, os, glob, shutil, tempfile
        from urllib.parse import urlparse
        host = urlparse(site_url).netloc

        profiles = []
        if sys.platform == "darwin":
            profiles = glob.glob(os.path.expanduser(
                "~/Library/Application Support/Firefox/Profiles/*/cookies.sqlite"))
        elif sys.platform.startswith("linux"):
            profiles = glob.glob(os.path.expanduser(
                "~/.mozilla/firefox/*/cookies.sqlite"))
        elif sys.platform == "win32":
            profiles = glob.glob(os.path.expandvars(
                r"%APPDATA%\Mozilla\Firefox\Profiles\*\cookies.sqlite"))

        for db_path in profiles:
            tmp = shutil.copy(db_path, tempfile.mktemp(suffix=".sqlite"))
            try:
                conn = sqlite3.connect(tmp)
                row = conn.execute(
                    "SELECT value FROM moz_cookies WHERE host LIKE ? AND name='datadome' ORDER BY lastAccessed DESC LIMIT 1",
                    (f"%{host}%",)
                ).fetchone()
                conn.close()
                if row:
                    print(f"[firefox] ✓ found datadome cookie ({len(row[0])} chars)")
                    return row[0]
            except Exception:
                pass
            finally:
                os.unlink(tmp)
    except Exception as e:
        print(f"[firefox] failed: {e}")
    return ""


def send_to_server(cookie: str, server_url: str, service_key: str) -> bool:
    data = json.dumps({"cookie": cookie}).encode()
    req = urllib.request.Request(
        f"{server_url}/v1/cookies/datadome",
        data=data,
        headers={
            "Content-Type": "application/json",
            "X-Service-Key": service_key,
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            result = json.loads(resp.read())
            print(f"[server] ✓ {result.get('message', result)}")
            return True
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        print(f"[server] HTTP {e.code}: {body}")
    except Exception as e:
        print(f"[server] error: {e}")
    return False


def main():
    print(f"[sync] site:   {SITE_URL}")
    print(f"[sync] server: {SERVER_URL}")
    print()

    # Try Chrome first, then Firefox
    cookie = get_cookie_from_chrome(SITE_URL)
    if not cookie:
        print("[sync] trying Firefox...")
        cookie = get_cookie_from_firefox(SITE_URL)

    if not cookie:
        print("\n✗ No cookie found.")
        print(f"  → Open Chrome and visit {SITE_URL}, then run this script again.")
        sys.exit(1)

    print()
    ok = send_to_server(cookie, SERVER_URL, SERVICE_KEY)
    if ok:
        print("\n✓ Cookie synced. Server will use it for all scrapes.")
    else:
        print("\n✗ Failed to send cookie to server.")
        sys.exit(1)


if __name__ == "__main__":
    main()
