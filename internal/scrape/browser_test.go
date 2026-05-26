package scrape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectChromeExecPathUsesConfiguredBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser")
	if err := os.WriteFile(path, []byte("browser"), 0o755); err != nil {
		t.Fatalf("create browser fixture: %v", err)
	}
	t.Setenv("CHROME_BIN", path)
	t.Setenv("CHROME_PATH", "")

	if got := detectChromeExecPath(); got != path {
		t.Fatalf("expected configured browser %q, got %q", path, got)
	}
}

func TestNewPlaywrightBrowserUsesValidNpxPackageInvocation(t *testing.T) {
	command := NewPlaywrightBrowser("").Command
	if !strings.Contains(command, "npx --yes --package=playwright -- node -e") {
		t.Fatalf("unexpected default Playwright command: %s", command)
	}
}
