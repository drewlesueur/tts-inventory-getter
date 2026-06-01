package scrape

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/dom"
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

func TestNewPlaywrightBrowserUsesLocalPlaywrightDependency(t *testing.T) {
	command := NewPlaywrightBrowser("").Command
	if !strings.Contains(command, "node -e") || !strings.Contains(command, `require("playwright")`) {
		t.Fatalf("unexpected default Playwright command: %s", command)
	}
}

func TestShouldFallbackToNpxPlaywright(t *testing.T) {
	errText := `Error: Cannot find module 'playwright'`
	if !shouldFallbackToNpxPlaywright(errText) {
		t.Fatalf("expected fallback trigger for missing playwright module")
	}
	if shouldFallbackToNpxPlaywright("some unrelated node error") {
		t.Fatalf("unexpected fallback trigger for unrelated error")
	}
}

func TestChromeBrowserErrorfSuppressesAdoptedStyleSheetsEvent(t *testing.T) {
	var output bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	chromeBrowserErrorf("unhandled node event %T", &dom.EventAdoptedStyleSheetsModified{})
	if output.Len() != 0 {
		t.Fatalf("expected stylesheet event to be suppressed, got %q", output.String())
	}

	chromeBrowserErrorf("browser warning %s", "kept")
	if !strings.Contains(output.String(), "ERROR: browser warning kept") {
		t.Fatalf("expected other browser errors to remain logged, got %q", output.String())
	}
}
