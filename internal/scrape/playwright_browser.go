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

func NewPlaywrightBrowser(command string) *PlaywrightBrowser {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "node scripts/playwright_render.js"
	}
	return &PlaywrightBrowser{Command: command}
}

func (p PlaywrightBrowser) Render(ctx context.Context, urlStr string, _ config.SiteConfig) (string, error) {
	if strings.TrimSpace(p.Command) == "" {
		return "", fmt.Errorf("playwright command missing")
	}
	script := p.Command + " " + strconv.Quote(urlStr)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", script)
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("playwright render failed: %w stderr=%s", err, strings.TrimSpace(errOut.String()))
	}
	html := out.String()
	if strings.TrimSpace(html) == "" {
		return "", fmt.Errorf("playwright returned empty html")
	}
	return html, nil
}
