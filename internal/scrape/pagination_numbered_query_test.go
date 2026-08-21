package scrape

import (
	"context"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// Numbered widgets elide the middle ("1 2 3 … 20"), so following only the
// rendered links strands whole pages. The gaps must be synthesized — and only
// the gaps, since re-emitting a rendered link would not string-match it
// (url.Values.Encode reorders the query) and would waste the maxPages budget.
func TestExtractNumberedPageURLs_FillsOnlyElidedGaps(t *testing.T) {
	widget := `<html><body><ul class="inventory-pagination pagination">
	  <li><a href="/inventory/new/ford?instock=true&amp;page=1">1</a></li>
	  <li><a href="/inventory/new/ford?instock=true&amp;page=2">2</a></li>
	  <li><a href="/inventory/new/ford?instock=true&amp;page=3">3</a></li>
	  <li><span>…</span></li>
	  <li><a href="/inventory/new/ford?instock=true&amp;page=6">6</a></li>
	</ul></body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(widget))
	if err != nil {
		t.Fatal(err)
	}
	got := extractNumberedPageURLs("https://dealer.test/inventory", doc)
	if len(got) != 2 {
		t.Fatalf("expected the 2 elided pages, got %d: %v", len(got), got)
	}
	for i, want := range []string{"page=4", "page=5"} {
		if !strings.Contains(got[i], want) {
			t.Fatalf("got[%d] = %q, want %s", i, got[i], want)
		}
	}
	// Built on the widget's own path, not the requested one.
	for _, u := range got {
		if !strings.Contains(u, "/inventory/new/ford") || !strings.Contains(u, "instock=true") {
			t.Fatalf("synthesized url dropped the canonical form: %q", u)
		}
	}
}

// Re-deriving the set from a later page is redundant work.
func TestExtractNumberedPageURLs_SkipsWhenAlreadyOnNumberedPage(t *testing.T) {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<ul class="pagination"><a href="/inventory?page=9">9</a></ul>`))
	if got := extractNumberedPageURLs("https://dealer.test/inventory?page=3", doc); got != nil {
		t.Fatalf("expected nil on a numbered page, got %v", got)
	}
}

// End to end: an elided widget must still yield every page's inventory.
func TestScrapeOnce_WalksElidedNumberedPagination(t *testing.T) {
	widget := `<ul class="pagination">` +
		`<a href="/inventory?page=1">1</a><a href="/inventory?page=2">2</a>` +
		`<span>…</span><a href="/inventory?page=4">4</a></ul>`
	card := func(n string) string {
		return `<div class="card"><a href="/viewdetails/new/car` + n + `/x">c` + n + `</a>` +
			`<h2>2026 Ford Model ` + n + `</h2><span class="price">$1` + n + `,000</span></div>`
	}
	site := config.SiteConfig{}
	site.ListPage.CardSelector = ".card"
	site.ListPage.TitleSelector = "h2"
	site.ListPage.URLSelector = "a"
	site.ListPage.PriceSelector = ".price"
	site.ListPage.Pagination.MaxPages = 10

	svc := Service{
		Fetcher: pageFetcher{pages: map[string]string{
			"https://dealer.test/inventory":        `<html><body>` + card("1") + widget + `</body></html>`,
			"https://dealer.test/inventory?page=2": `<html><body>` + card("2") + widget + `</body></html>`,
			"https://dealer.test/inventory?page=3": `<html><body>` + card("3") + widget + `</body></html>`,
			"https://dealer.test/inventory?page=4": `<html><body>` + card("4") + widget + `</body></html>`,
		}},
		DetailFetcher: HTMLDetailFetcher{Fetcher: pageFetcher{}},
		Extractors:    []Extractor{DOMExtractor{}},
		Concurrency:   1,
	}
	res := svc.ScrapeOnce(context.Background(), "https://dealer.test/inventory", site)
	// page 3 is reachable only via synthesis
	if len(res.Items) != 4 {
		t.Fatalf("expected 4 items across the elided walk got %d errs=%+v", len(res.Items), res.Errors)
	}
}
