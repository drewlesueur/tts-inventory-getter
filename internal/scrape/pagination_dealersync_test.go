package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestExtractNextPageURLs_UsesDealerSyncAiPage(t *testing.T) {
	html := `<html><body>
<div id="ds-inventory-model" data-results-count="15" data-results-index="0" data-results-total="52"></div>
</body></html>`
	next := extractNextPageURLs("https://dealer.test/pre-owned-cars", html, config.SiteConfig{})
	if len(next) == 0 {
		t.Fatalf("expected next page url")
	}
	if next[0] != "https://dealer.test/pre-owned-cars?ai_page=1" {
		t.Fatalf("unexpected next url: %s", next[0])
	}
}

func TestExtractNextPageURLs_StopsOnLastDealerSyncPage(t *testing.T) {
	html := `<html><body>
<div id="ds-inventory-model" data-results-count="15" data-results-index="45" data-results-total="52"></div>
</body></html>`
	next := extractNextPageURLs("https://dealer.test/pre-owned-cars?ai_page=3", html, config.SiteConfig{})
	if len(next) != 0 {
		t.Fatalf("expected no next page on last page, got %+v", next)
	}
}
