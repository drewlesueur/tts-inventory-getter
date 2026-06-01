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

const defaultPlaywrightCommand = `node -e 'const { chromium } = require("playwright"); (async () => { const browser = await chromium.launch({ headless: true }); const page = await browser.newPage(); await page.goto(process.argv[1], { waitUntil: "networkidle" }); await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight)); await new Promise(r => setTimeout(r, 900)); process.stdout.write(await page.content()); await browser.close(); })().catch((e) => { console.error(e); process.exit(1); });'`
const npxPlaywrightCommand = `npx --yes -p playwright node -e 'const { chromium } = require("playwright"); (async () => { const browser = await chromium.launch({ headless: true }); const page = await browser.newPage(); await page.goto(process.argv[1], { waitUntil: "networkidle" }); await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight)); await new Promise(r => setTimeout(r, 900)); process.stdout.write(await page.content()); await browser.close(); })().catch((e) => { console.error(e); process.exit(1); });'`

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
	if err != nil && shouldFallbackToNpxPlaywright(stderr) && strings.TrimSpace(p.Command) == defaultPlaywrightCommand {
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

func shouldFallbackToNpxPlaywright(stderr string) bool {
	e := strings.ToLower(stderr)
	return strings.Contains(e, "cannot find module 'playwright'") || strings.Contains(e, `cannot find module "playwright"`)
}
