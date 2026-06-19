package scrape

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// PlaywrightBrowser shells out to a local playwright command.
// Command should print full HTML to stdout.
type PlaywrightBrowser struct {
	Command string
}

// stealthScript uses playwright-extra + puppeteer-extra-plugin-stealth which patches
// navigator.webdriver, chrome runtime, plugins, canvas, WebGL and more.
const stealthScript = `
const { chromium } = require("playwright-extra");
const StealthPlugin = require("puppeteer-extra-plugin-stealth");
chromium.use(StealthPlugin());
(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ["--disable-blink-features=AutomationControlled", "--no-sandbox", "--disable-setuid-sandbox"]
  });
  const ctx = await browser.newContext({
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    viewport: { width: 1366, height: 768 },
    locale: "en-US",
    extraHTTPHeaders: {
      "Accept-Language": "en-US,en;q=0.9",
      "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"
    }
  });
  const page = await ctx.newPage();
  await page.goto(process.argv[1], { waitUntil: "networkidle" });
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await new Promise(r => setTimeout(r, 900));
  process.stdout.write(await page.content());
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
`

// fallbackStealthScript is used when playwright-extra is unavailable.
const fallbackStealthScript = `
const { chromium } = require("playwright");
(async () => {
  const browser = await chromium.launch({
    headless: true,
    args: ["--disable-blink-features=AutomationControlled", "--no-sandbox", "--disable-setuid-sandbox"]
  });
  const ctx = await browser.newContext({
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
    viewport: { width: 1366, height: 768 },
    locale: "en-US"
  });
  const page = await ctx.newPage();
  await page.addInitScript(() => { Object.defineProperty(navigator, "webdriver", { get: () => undefined }); });
  await page.goto(process.argv[1], { waitUntil: "networkidle" });
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await new Promise(r => setTimeout(r, 900));
  process.stdout.write(await page.content());
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
`

const defaultPlaywrightCommand = "node -e '" + stealthScript + "'"
const fallbackPlaywrightCommand = "node -e '" + fallbackStealthScript + "'"
const npxPlaywrightCommand = "npx --yes -p playwright node -e '" + fallbackStealthScript + "'"

func NewPlaywrightBrowser(command string) *PlaywrightBrowser {
	command = strings.TrimSpace(command)
	if command == "" {
		command = defaultPlaywrightCommand
	}
	return &PlaywrightBrowser{Command: command}
}

func (p PlaywrightBrowser) Render(ctx context.Context, urlStr string, _ config.SiteConfig) (string, error) {
	if strings.TrimSpace(p.Command) == "" {
		return "", fmt.Errorf("playwright command missing")
	}
	html, stderr, err := runPlaywrightCommand(ctx, p.Command, urlStr)

	// playwright-extra or stealth plugin not installed → fall back to plain playwright
	if err != nil && isModuleNotFound(stderr, "playwright-extra", "puppeteer-extra-plugin-stealth") &&
		strings.TrimSpace(p.Command) == defaultPlaywrightCommand {
		html, stderr, err = runPlaywrightCommand(ctx, fallbackPlaywrightCommand, urlStr)
	}

	// plain playwright not installed locally → try npx
	if err != nil && isModuleNotFound(stderr, "playwright") &&
		strings.TrimSpace(p.Command) == defaultPlaywrightCommand {
		html, stderr, err = runPlaywrightCommand(ctx, npxPlaywrightCommand, urlStr)
	}

	if err != nil {
		return "", fmt.Errorf("playwright render failed: %w stderr=%s", err, strings.TrimSpace(stderr))
	}
	if strings.TrimSpace(html) == "" {
		return "", fmt.Errorf("playwright returned empty html")
	}
	return html, nil
}

func runPlaywrightCommand(ctx context.Context, command, urlStr string) (string, string, error) {
	script := command + " " + strconv.Quote(urlStr)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", script)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func isModuleNotFound(stderr string, modules ...string) bool {
	e := strings.ToLower(stderr)
	if !strings.Contains(e, "cannot find module") {
		return false
	}
	for _, m := range modules {
		if strings.Contains(e, "'"+m+"'") || strings.Contains(e, `"`+m+`"`) {
			return true
		}
	}
	return false
}
