#!/usr/bin/env python3
"""
DataDome cookie refresher — tries multiple strategies in order.

Strategy 1: curl_cffi   — Chrome TLS + HTTP/2 C-level fingerprint impersonation
Strategy 2: patchright  — patched Playwright (binary-level Chrome patches)
Strategy 3: nodriver    — undetected Chrome via CDP

Usage:
  python3.11 refresh_cookie.py <url>
  python3.11 refresh_cookie.py https://www.saiautosale.com/cars-for-sale

Output (stdout): {"datadome": "<value>"}
        or       {"error": "<reason>"}
"""
import sys
import json
import re
import asyncio
from typing import Optional

TARGET_URL = sys.argv[1] if len(sys.argv) > 1 else "https://www.saiautosale.com/cars-for-sale"

CHROME_UA = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
    "AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/124.0.0.0 Safari/537.36"
)

HEADERS = {
    "User-Agent": CHROME_UA,
    "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
    "Accept-Language": "en-US,en;q=0.9",
    "sec-ch-ua": '"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"',
    "sec-ch-ua-mobile": "?0",
    "sec-ch-ua-platform": '"Windows"',
    "sec-fetch-dest": "document",
    "sec-fetch-mode": "navigate",
    "sec-fetch-site": "none",
    "sec-fetch-user": "?1",
    "upgrade-insecure-requests": "1",
}


def is_blocked(html: str) -> bool:
    return "captcha-delivery.com" in html or (
        "datadome" in html and "captcha" in html
    )


def extract_dd_params(html: str) -> dict:
    params = {}
    for m in re.finditer(r"'(\w+)'\s*:\s*'([^']*)'", html):
        params[m.group(1)] = m.group(2)
    for m in re.finditer(r"'(\w+)'\s*:\s*(\d+)", html):
        if m.group(1) not in params:
            params[m.group(1)] = m.group(2)
    return params


# ─── Strategy 1: curl_cffi ───────────────────────────────────────────────────
def try_curl_cffi(url: str) -> Optional[str]:
    try:
        from curl_cffi import requests as cffi
    except ImportError:
        print("[curl_cffi] not installed", file=sys.stderr)
        return None

    session = cffi.Session(impersonate="chrome124")

    try:
        resp = session.get(url, headers=HEADERS, timeout=30)
    except Exception as e:
        print(f"[curl_cffi] error: {e}", file=sys.stderr)
        return None

    cookie = resp.cookies.get("datadome")
    params = extract_dd_params(resp.text)
    rt = params.get("rt", "none")

    if not is_blocked(resp.text):
        print("[curl_cffi] ✓ passed directly", file=sys.stderr)
        return cookie

    print(f"[curl_cffi] blocked rt={rt}", file=sys.stderr)

    # retry with the initial cookie DataDome set
    if cookie:
        try:
            resp2 = session.get(url, headers={
                **HEADERS,
                "Cookie": f"datadome={cookie}",
                "sec-fetch-site": "same-origin",
                "Referer": url,
            }, timeout=30)
            new_cookie = resp2.cookies.get("datadome") or cookie
            if not is_blocked(resp2.text):
                print("[curl_cffi] ✓ passed on cookie retry", file=sys.stderr)
                return new_cookie
        except Exception as e:
            print(f"[curl_cffi] retry error: {e}", file=sys.stderr)

    return None


# ─── Strategy 2: patchright (patched Playwright) ─────────────────────────────
async def try_patchright(url: str) -> Optional[str]:
    try:
        from patchright.async_api import async_playwright
    except ImportError:
        print("[patchright] not installed", file=sys.stderr)
        return None

    print("[patchright] launching...", file=sys.stderr)
    async with async_playwright() as p:
        browser = await p.chromium.launch(
            headless=True,
            args=["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
        )
        ctx = await browser.new_context(
            user_agent=CHROME_UA,
            viewport={"width": 1366, "height": 768},
            locale="en-US",
            extra_http_headers={
                "Accept-Language": "en-US,en;q=0.9",
                "sec-ch-ua": '"Chromium";v="124", "Google Chrome";v="124", "Not-A.Brand";v="99"',
                "sec-ch-ua-mobile": "?0",
                "sec-ch-ua-platform": '"Windows"',
            },
        )
        page = await ctx.new_page()

        try:
            await page.goto(url, wait_until="domcontentloaded", timeout=30000)

            for attempt in range(8):
                await asyncio.sleep(3)
                content = await page.content()
                if not is_blocked(content):
                    print(f"[patchright] ✓ passed after {(attempt+1)*3}s", file=sys.stderr)
                    break
                rt = extract_dd_params(content).get("rt", "?")
                print(f"[patchright] still blocked rt={rt} at {(attempt+1)*3}s", file=sys.stderr)

            cookies = await ctx.cookies()
            for c in cookies:
                if c["name"] == "datadome":
                    await browser.close()
                    return c["value"]

        except Exception as e:
            print(f"[patchright] error: {e}", file=sys.stderr)
        finally:
            await browser.close()

    return None


# ─── Strategy 3: nodriver ────────────────────────────────────────────────────
async def try_nodriver(url: str) -> Optional[str]:
    try:
        import nodriver as uc
    except ImportError:
        print("[nodriver] not installed", file=sys.stderr)
        return None

    print("[nodriver] launching undetected Chrome...", file=sys.stderr)
    browser = None
    try:
        browser = await uc.start(
            headless=True,
            browser_args=["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
        )
        page = await browser.get(url)

        for attempt in range(8):
            await asyncio.sleep(3)
            html = await page.get_content()
            if not is_blocked(html):
                print(f"[nodriver] ✓ passed after {(attempt+1)*3}s", file=sys.stderr)
                break
            rt = extract_dd_params(html).get("rt", "?")
            print(f"[nodriver] still blocked rt={rt} at {(attempt+1)*3}s", file=sys.stderr)

        # Extract via CDP
        import nodriver.cdp.storage as storage
        cookies = await page.send(storage.get_cookies())
        for c in cookies:
            if c.name == "datadome":
                return c.value

    except Exception as e:
        print(f"[nodriver] error: {e}", file=sys.stderr)
    finally:
        if browser:
            try:
                browser.stop()
            except Exception:
                pass

    return None


# ─── Main ─────────────────────────────────────────────────────────────────────
def main():
    print(f"[main] target: {TARGET_URL}", file=sys.stderr)

    # Strategy 1
    cookie = try_curl_cffi(TARGET_URL)
    if cookie:
        print(json.dumps({"datadome": cookie}))
        return

    # Strategy 2
    print("[main] trying patchright...", file=sys.stderr)
    cookie = asyncio.run(try_patchright(TARGET_URL))
    if cookie:
        print(json.dumps({"datadome": cookie}))
        return

    # Strategy 3
    print("[main] trying nodriver...", file=sys.stderr)
    cookie = asyncio.run(try_nodriver(TARGET_URL))
    if cookie:
        print(json.dumps({"datadome": cookie}))
        return

    print(json.dumps({"error": "all strategies failed"}))
    sys.exit(1)


if __name__ == "__main__":
    main()
