#!/usr/bin/env python3
"""
fetch_details.py — Fetch multiple detail pages in ONE Camoufox session.

Reads a JSON array of URLs from stdin, opens a single Camoufox browser, navigates
to each URL reusing the same browser (fast — one launch for N pages), and streams
results as NDJSON so a caller that kills us on deadline keeps everything fetched
so far.

Usage:  echo '["url1","url2"]' | python3.11 fetch_details.py [datadome_cookie]

Stdout: one JSON object per line:
  {"url": "...", "html": "..."}   as each page completes
  {"done": true, "cookie": "..."} final line on clean exit
Stderr: debug info
"""
import sys
import json
import asyncio


def emit(obj):
    print(json.dumps(obj))
    sys.stdout.flush()


def is_blocked(html: str) -> bool:
    if "captcha-delivery.com" in html:
        return True
    if len(html) < 4000 and "Please enable JS and disable any ad blocker" in html:
        return True
    return False


def _proxy_from_env():
    import os
    from urllib.parse import urlparse
    raw = os.environ.get("SCRAPER_PROXY", "").strip()
    if not raw:
        return None
    u = urlparse(raw)
    proxy = {"server": f"{u.scheme}://{u.hostname}:{u.port}"}
    if u.username:
        proxy["username"] = u.username
    if u.password:
        proxy["password"] = u.password
    return proxy


def new_camoufox():
    """AsyncCamoufox with a pre-generated fingerprint (bypasses headless-server
    screen-detection that breaks browserforge header generation). Honors
    SCRAPER_PROXY for residential-proxy routing."""
    from camoufox.async_api import AsyncCamoufox
    from browserforge.fingerprints import FingerprintGenerator, Screen
    fp = FingerprintGenerator(
        browser="firefox",
        os=("windows", "macos", "linux"),
        device="desktop",
        screen=Screen(min_width=1280, min_height=720),
    ).generate()
    kwargs = {"headless": True, "fingerprint": fp, "i_know_what_im_doing": True}
    proxy = _proxy_from_env()
    if proxy:
        kwargs["proxy"] = proxy
        print(f"[details] using proxy {proxy['server']}", file=sys.stderr)
    return AsyncCamoufox(**kwargs)


DETAIL_HEADERS = {
    "User-Agent": ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
                   "(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
    "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
    "Accept-Language": "en-US,en;q=0.9",
}


def curl_fetch_one(url, cookie=""):
    """Fast detail fetch via curl_cffi (Chrome TLS). Returns (url, html|None)."""
    try:
        from curl_cffi import requests as cffi
        s = cffi.Session(impersonate="chrome124")
        headers = dict(DETAIL_HEADERS)
        if cookie:
            headers["Cookie"] = f"datadome={cookie}"
        r = s.get(url, headers=headers, timeout=30)
        if r.status_code < 400 and not is_blocked(r.text):
            return url, r.text
    except Exception as e:
        print(f"[details] curl_cffi err {url}: {e}", file=sys.stderr)
    return url, None


async def camoufox_fetch(urls):
    """Render the given (DataDome-blocked) detail URLs in one Camoufox session."""
    results, dd_cookie = {}, ""
    async with new_camoufox() as browser:
        page = await browser.new_page()
        from urllib.parse import urlparse
        try:
            first = urlparse(urls[0])
            await page.goto(f"{first.scheme}://{first.netloc}/", wait_until="domcontentloaded", timeout=30000)
            for _ in range(5):
                await asyncio.sleep(2)
                if not is_blocked(await page.content()):
                    break
        except Exception as e:
            print(f"[details] warm-up skipped: {e}", file=sys.stderr)

        for i, url in enumerate(urls):
            try:
                await page.goto(url, wait_until="domcontentloaded", timeout=30000)
                html = ""
                for _ in range(6):
                    await asyncio.sleep(2)
                    html = await page.content()
                    if not is_blocked(html):
                        break
                try:
                    await page.wait_for_selector(".vdp-info-block__info-item-title", timeout=10000)
                except Exception:
                    pass
                try:
                    await page.wait_for_load_state("networkidle", timeout=6000)
                except Exception:
                    pass
                results[url] = await page.content()
                emit({"url": url, "html": results[url]})
                print(f"[details] camoufox {i+1}/{len(urls)} ok {url}", file=sys.stderr)
            except Exception as e:
                print(f"[details] camoufox FAILED {url}: {e}", file=sys.stderr)
                results[url] = ""
                emit({"url": url, "html": ""})
        try:
            cookies = await page.context.cookies()
            dd_cookie = next((c["value"] for c in cookies if c["name"] == "datadome"), "")
        except Exception:
            pass
    return results, dd_cookie


async def fetch_all(urls, cookie=""):
    import concurrent.futures
    import functools
    results = {}
    blocked = []

    # 1. Fast path: curl_cffi concurrently (non-DataDome sites need no browser).
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as ex:
        for url, html in ex.map(functools.partial(curl_fetch_one, cookie=cookie), urls):
            if html:
                results[url] = html
                emit({"url": url, "html": html})
            else:
                blocked.append(url)
    print(f"[details] curl_cffi got {len(results)}/{len(urls)}; {len(blocked)} need browser", file=sys.stderr)

    # 2. Slow path: Camoufox only for URLs that were blocked/failed.
    dd_cookie = ""
    if blocked:
        try:
            import camoufox.async_api  # noqa: F401
            cam_results, dd_cookie = await camoufox_fetch(blocked)
            results.update(cam_results)
        except ImportError:
            print("[details] camoufox not installed; leaving blocked urls empty", file=sys.stderr)
            for u in blocked:
                results.setdefault(u, "")

    return results, dd_cookie


def main():
    import os
    cookie = sys.argv[1] if len(sys.argv) > 1 else os.environ.get("DATADOME_COOKIE", "")
    raw = sys.stdin.read().strip()
    if not raw:
        emit({"error": "no urls on stdin"})
        sys.exit(1)
    try:
        urls = json.loads(raw)
    except Exception as e:
        emit({"error": f"bad json input: {e}"})
        sys.exit(1)
    if not isinstance(urls, list) or not urls:
        emit({"error": "expected non-empty JSON array of urls"})
        sys.exit(1)

    _, dd_cookie = asyncio.run(fetch_all(urls, cookie))
    emit({"done": True, "cookie": dd_cookie})


if __name__ == "__main__":
    main()
