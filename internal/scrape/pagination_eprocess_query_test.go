package scrape

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// Dealer eProcess wraps its pager in <div class="search-pagination"> around an
// unclassed <ul>, and numbers pages ?p=N. Both broke the walk: the element
// specific selectors (nav/ul) matched nothing, and the numbered synthesizer
// only understood ?page=N, so hornemazdaavondale collected 12 of 298 vehicles.
const eProcessNumberedPager = `<div class="srp_pagination_links_container">
  <div class="search-pagination"><ul>
    <li class="active"><span>1</span></li>
    <li><a href="/search/avondale-az/?cy=85323&amp;p=2">2</a></li>
    <li><a href="/search/avondale-az/?cy=85323&amp;p=3">3</a></li>
    <li>...</li>
    <li><a href="/search/avondale-az/?cy=85323&amp;p=6">6</a></li>
    <li class="next"><a href="/search/avondale-az/?cy=85323&amp;p=2"></a></li>
  </ul></div>
</div>`

func TestExtractNextPageURLs_FindsDivWrappedEProcessPager(t *testing.T) {
	got := extractNextPageURLs("https://dealer.test/search/", eProcessNumberedPager, config.SiteConfig{})
	for _, want := range []string{"p=2", "p=3", "p=6"} {
		found := false
		for _, u := range got {
			if strings.Contains(u, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("rendered page link %s missing from %v", want, got)
		}
	}
	// The elided 4 and 5 must be synthesized on the widget's own ?p= parameter.
	for _, want := range []string{"p=4", "p=5"} {
		found := false
		for _, u := range got {
			if strings.Contains(u, want) && !strings.Contains(u, "page=") {
				found = true
			}
		}
		if !found {
			t.Fatalf("elided page %s missing or written with the wrong param: %v", want, got)
		}
	}
}

// Writing "page" onto an eProcess "?p=2" link yields "?p=2&page=4", which the
// server answers with page 2 — the gap silently never gets walked.
func TestExtractNumberedPageURLs_KeepsWidgetPageParam(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(eProcessNumberedPager))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range extractNumberedPageURLs("https://dealer.test/search/", doc) {
		if strings.Contains(u, "page=") {
			t.Fatalf("synthesized url used the wrong parameter: %q", u)
		}
		if !strings.Contains(u, "cy=85323") {
			t.Fatalf("synthesized url dropped the widget's canonical query: %q", u)
		}
	}
}

// The ?page=N platforms must keep working unchanged.
func TestExtractNumberedPageURLs_StillHandlesPageParam(t *testing.T) {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<ul class="pagination"><li><a href="/inventory?page=2">2</a></li>` +
			`<li>…</li><li><a href="/inventory?page=5">5</a></li></ul>`))
	got := extractNumberedPageURLs("https://dealer.test/inventory", doc)
	if len(got) != 2 {
		t.Fatalf("expected the 2 elided pages, got %v", got)
	}
	for i, want := range []string{"page=3", "page=4"} {
		if !strings.Contains(got[i], want) {
			t.Fatalf("got[%d] = %q, want %s", i, got[i], want)
		}
	}
}
