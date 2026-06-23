#!/usr/bin/env python3
"""
fetch_page.py — Fetch a page bypassing DataDome.

Strategy 1 (fast):    curl_cffi  — Chrome TLS + HTTP/2 fingerprint with existing cookie
Strategy 2 (fallback): Camoufox  — Firefox with C++ level patches, bypasses DataDome
                                     without any cookie or proxy

Usage: python3.11 fetch_page.py <url> [datadome_cookie]

Stdout: JSON {"html": "...", "cookie": "refreshed_datadome_value", "status": 200}
Stderr: debug info
Exit 0 on success, 1 on failure.
"""
import sys
import json
import asyncio

url    = sys.argv[1] if len(sys.argv) > 1 else ""
cookie = sys.argv[2] if len(sys.argv) > 2 else ""

if not url:
    print(json.dumps({"error": "url required"}))
    sys.exit(1)

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
    # captcha-delivery.com is the only reliable DataDome block marker. A real page
    # can mention "datadome"/"captcha" benignly, so don't use those as a heuristic.
    if "captcha-delivery.com" in html:
        return True
    if len(html) < 4000 and "Please enable JS and disable any ad blocker" in html:
        return True
    return False


def new_camoufox():
    """Build an AsyncCamoufox with a pre-generated browserforge fingerprint.

    On headless servers camoufox's automatic screen detection (get_monitors)
    can produce a screen constraint that browserforge can't satisfy
    ("No headers based on this input can be generated"). Generating the
    fingerprint ourselves (no screen constraint) bypasses that.
    """
    from camoufox.async_api import AsyncCamoufox
    from browserforge.fingerprints import FingerprintGenerator, Screen
    # device="desktop" + a desktop-sized screen guarantees the site serves its
    # desktop layout (a mobile fingerprint would yield different selectors → 0 cards).
    fp = FingerprintGenerator(
        browser="firefox",
        os=("windows", "macos", "linux"),
        device="desktop",
        screen=Screen(min_width=1280, min_height=720),
    ).generate()
    return AsyncCamoufox(headless=True, fingerprint=fp, i_know_what_im_doing=True)


# ── Strategy 1: curl_cffi (fast, uses existing cookie) ───────────────────────
def try_curl_cffi(url: str, cookie: str):
    try:
        from curl_cffi import requests as cffi
    except ImportError:
        return None, None

    session = cffi.Session(impersonate="chrome124")
    headers = dict(HEADERS)
    if cookie:
        headers["Cookie"] = f"datadome={cookie}"

    try:
        resp = session.get(url, headers=headers, timeout=30)
    except Exception as e:
        print(f"[curl_cffi] error: {e}", file=sys.stderr)
        return None, None

    new_cookie = resp.cookies.get("datadome", "")

    if resp.status_code >= 400 or is_blocked(resp.text):
        print(f"[curl_cffi] blocked/error status={resp.status_code}", file=sys.stderr)
        return None, None

    print("[curl_cffi] ✓ success", file=sys.stderr)
    return resp.text, new_cookie or cookie


# ── Strategy 2: Camoufox (reliable, C++ level Firefox patches) ───────────────
async def try_camoufox(url: str):
    try:
        from camoufox.async_api import AsyncCamoufox
    except ImportError:
        print("[camoufox] not installed: pip install camoufox", file=sys.stderr)
        return None, None

    print("[camoufox] launching Firefox...", file=sys.stderr)
    try:
        async with new_camoufox() as browser:
            page = await browser.new_page()
            await page.goto(url, wait_until="domcontentloaded")

            # Wait for DataDome to clear (usually instant with Camoufox)
            passed = False
            for attempt in range(8):
                await asyncio.sleep(3)
                content = await page.content()
                if not is_blocked(content):
                    print(f"[camoufox] ✓ passed after {(attempt+1)*3}s", file=sys.stderr)
                    passed = True
                    break
                print(f"[camoufox] still blocked at {(attempt+1)*3}s...", file=sys.stderr)

            if passed:
                # Scroll to trigger lazy-loaded cards, then wait for network idle.
                try:
                    prev_count = -1
                    for _ in range(10):
                        await page.evaluate("() => { if (document.body) window.scrollTo(0, document.body.scrollHeight); }")
                        await asyncio.sleep(1.2)
                        count = await page.evaluate(
                            "() => document.querySelectorAll('li.vehicle-snapshot, .vehicle-snapshot, [class*=\"vehicle-card\"], article').length"
                        )
                        if count == prev_count:
                            break
                        prev_count = count
                    try:
                        await page.wait_for_load_state("networkidle", timeout=5000)
                    except Exception:
                        pass
                    print(f"[camoufox] cards after scroll: {prev_count}", file=sys.stderr)
                except Exception as e:
                    print(f"[camoufox] scroll error: {e}", file=sys.stderr)

                content = await page.content()
                cookies = await page.context.cookies()
                dd_cookie = next(
                    (c["value"] for c in cookies if c["name"] == "datadome"), ""
                )
                return content, dd_cookie

    except Exception as e:
        print(f"[camoufox] error: {e}", file=sys.stderr)

    return None, None


# ── Main ──────────────────────────────────────────────────────────────────────
def main():
    # Strategy 1: fast path with existing cookie
    if cookie:
        html, new_cookie = try_curl_cffi(url, cookie)
        if html:
            print(json.dumps({"html": html, "cookie": new_cookie or cookie, "status": 200}))
            return

    # Strategy 2: Camoufox (works without any cookie, from any IP)
    print("[main] falling back to Camoufox...", file=sys.stderr)
    html, new_cookie = asyncio.run(try_camoufox(url))
    if html:
        print(json.dumps({"html": html, "cookie": new_cookie, "status": 200}))
        return

    print(json.dumps({"error": "all strategies failed"}))
    sys.exit(1)


if __name__ == "__main__":
    main()
