#!/usr/bin/env python3
"""
fetch_details.py — Fetch multiple detail pages in ONE Camoufox session.

Reads a JSON array of URLs from stdin, opens a single Camoufox browser, navigates
to each URL reusing the same browser (fast — one launch for N pages), and returns
a JSON object mapping url -> html.

Usage:  echo '["url1","url2"]' | python3.11 fetch_details.py

Stdout: JSON {"results": {"url1": "<html>", ...}, "cookie": "refreshed_datadome"}
Stderr: debug info
"""
import sys
import json
import asyncio


def is_blocked(html: str) -> bool:
    if "captcha-delivery.com" in html:
        return True
    if len(html) < 4000 and "Please enable JS and disable any ad blocker" in html:
        return True
    return False


async def fetch_all(urls):
    try:
        from camoufox.async_api import AsyncCamoufox
    except ImportError:
        print(json.dumps({"error": "camoufox not installed"}))
        sys.exit(1)

    results = {}
    dd_cookie = ""

    async with AsyncCamoufox(headless=True, os="windows") as browser:
        page = await browser.new_page()
        for i, url in enumerate(urls):
            try:
                await page.goto(url, wait_until="domcontentloaded", timeout=30000)
                # Wait for DataDome to clear if challenged
                html = ""
                for attempt in range(6):
                    await asyncio.sleep(2)
                    html = await page.content()
                    if not is_blocked(html):
                        break
                # Let the page fully hydrate (spec blocks, gallery) before capturing.
                try:
                    await page.wait_for_load_state("networkidle", timeout=8000)
                except Exception:
                    pass
                # Trigger the lazy-loaded photo carousel by scrolling it into view
                # and forcing data-lazy → captured markup.
                try:
                    await page.evaluate("""() => {
                        const g = document.querySelector('.image-carousel, .vdp-carousel-block');
                        if (g) g.scrollIntoView();
                        window.scrollTo(0, document.body.scrollHeight / 2);
                    }""")
                    await asyncio.sleep(1.5)
                except Exception:
                    pass
                await asyncio.sleep(1)
                html = await page.content()
                results[url] = html
                print(f"[details] {i+1}/{len(urls)} ok ({len(html)} bytes) {url}", file=sys.stderr)
            except Exception as e:
                print(f"[details] {i+1}/{len(urls)} FAILED {url}: {e}", file=sys.stderr)
                results[url] = ""

        # Capture refreshed cookie
        try:
            cookies = await page.context.cookies()
            dd_cookie = next((c["value"] for c in cookies if c["name"] == "datadome"), "")
        except Exception:
            pass

    return results, dd_cookie


def main():
    raw = sys.stdin.read().strip()
    if not raw:
        print(json.dumps({"error": "no urls on stdin"}))
        sys.exit(1)
    try:
        urls = json.loads(raw)
    except Exception as e:
        print(json.dumps({"error": f"bad json input: {e}"}))
        sys.exit(1)
    if not isinstance(urls, list) or not urls:
        print(json.dumps({"error": "expected non-empty JSON array of urls"}))
        sys.exit(1)

    results, cookie = asyncio.run(fetch_all(urls))
    print(json.dumps({"results": results, "cookie": cookie}))


if __name__ == "__main__":
    main()
