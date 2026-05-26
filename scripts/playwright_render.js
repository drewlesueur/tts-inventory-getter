const { chromium } = require("playwright");

const url = process.argv[2];
if (!url) {
  console.error("usage: node playwright_render.js <url>");
  process.exit(2);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    await page.goto(url, { waitUntil: "networkidle" });
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await new Promise((r) => setTimeout(r, 900));
    process.stdout.write(await page.content());
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exit(1);
});
