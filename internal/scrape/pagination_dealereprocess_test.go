package scrape

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

// The eProcess "carbon" pager is a text input and two JS arrow <div>s, so the
// generic href walkers see nothing. Page count and current page have to come
// from the widget, and the pages themselves from ?p=N.
const eProcessPager = `<div class="pagination_settings">
  <div class="pagination_settings__arrow" data-direction="left"></div>
  <label><span>Page</span><input name="pagination_settings_input" class="pagination_settings__input" type="text" value="1"></label>
  <span>of</span><span class="pagination_settings__page_count">7</span>
  <div class="pagination_settings__arrow" data-direction="right"></div>
</div>`

func TestExtractDealerEProcessPageURLs_SynthesizesEveryRemainingPage(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(eProcessPager))
	if err != nil {
		t.Fatal(err)
	}
	got := extractDealerEProcessPageURLs("https://dealer.test/search/used-town-nh/", doc)
	if len(got) != 6 {
		t.Fatalf("expected pages 2-7, got %d: %v", len(got), got)
	}
	for i, want := range []string{"p=2", "p=3", "p=4", "p=5", "p=6", "p=7"} {
		if !strings.Contains(got[i], want) {
			t.Fatalf("got[%d] = %q, want %s", i, got[i], want)
		}
	}
}

// Later pages must not re-emit the pages already walked, and the widget input
// cannot be trusted over the URL for that — it renders the served page.
func TestExtractDealerEProcessPageURLs_ContinuesFromRequestedPage(t *testing.T) {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(eProcessPager))
	got := extractDealerEProcessPageURLs("https://dealer.test/search/used-town-nh/?p=6", doc)
	if len(got) != 1 || !strings.Contains(got[0], "p=7") {
		t.Fatalf("expected only page 7, got %v", got)
	}
}

func TestExtractDealerEProcessPageURLs_SinglePage(t *testing.T) {
	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(
		`<div class="pagination_settings"><span class="pagination_settings__page_count">1</span></div>`))
	if got := extractDealerEProcessPageURLs("https://dealer.test/search/", doc); got != nil {
		t.Fatalf("expected no pages, got %v", got)
	}
}

// Guards the autosensenh config against the shape of a real Overfuel card.
// The dealer migrated off eProcess on 2026-09-24, so this no longer exercises
// the carbon theme (the pager tests above still do, for hornemazdaavondale).
// The fixture keeps the traits that broke naive selectors: the title is a bare
// h2 inside the link, the stock number is prefixed with a "Stock #" label and
// split by a React comment node, and mileage and price are anchor text.
func TestAutosenseConfigExtractsFullCard(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "sites",
		"urlkey_dXJsOjp3d3cuYXV0b3NlbnNlbmguY29tL3NlYXJjaC91c2VkLWNoaWNoZXN0ZXItbmg.yaml")
	site, err := (config.Loader{}).LoadByPath(path)
	if err != nil {
		t.Fatalf("load autosensenh config: %v", err)
	}
	html := `<div data-cy="vehicle-card" class="srp-card overflow-hidden h-100 conditionUsed card">
  <a data-cy="inventory-link" title="2017 Audi Q7 3.0T Premium Plus" href="/inventory/used-2017-audi-q7-3-0t-premium-plus-wa1laaf74hd031500-in-chichester-nh">
    <img class="img-srp d-block" src="https://static.overfuel.com/photos/2334/2018860/e3635c78-thumb.webp">
    <h2 class="h5 m-0 font-weight-bold text-truncate notranslate">2017 Audi Q7</h2>
  </a>
  <small class="opacity-75 srp-stocknum"><a href="/inventory/used-2017-audi-q7-3-0t-premium-plus-wa1laaf74hd031500-in-chichester-nh">Stock # <!-- -->N1587</a></small>
  <div class="srp-miles opacity-75 d-flex w-100 mt-1 col-12">
    <div class="text-truncate">3.0T Premium Plus</div>
    <div class="ps-2 text-nowrap ms-auto text-end"><a href="/inventory/used-2017-audi-q7-3-0t-premium-plus-wa1laaf74hd031500-in-chichester-nh">102,493<!-- --> <!-- -->miles</a></div>
  </div>
  <div class="d-flex align-items-center mb-3 border-top pt-2 srpPriceContainer">
    <span class="h4 font-weight-bold mt-3 label-price"><a href="/inventory/used-2017-audi-q7-3-0t-premium-plus-wa1laaf74hd031500-in-chichester-nh">$13,250</a></span>
  </div>
</div>`

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, site.BaseURL, site)
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("expected one vehicle, got items=%+v errors=%+v", items, errs)
	}
	it := items[0]
	if it.Title != "2017 Audi Q7" {
		t.Fatalf("title = %q", it.Title)
	}
	// The label and React comment node must not end up in the stock id.
	if it.StockID != "N1587" {
		t.Fatalf("stock = %q, want N1587 with the \"Stock #\" label stripped", it.StockID)
	}
	if it.Price != "$13,250" || !strings.HasPrefix(it.Mileage, "102,493") {
		t.Fatalf("price/mileage = %q/%q", it.Price, it.Mileage)
	}
	if !strings.HasPrefix(it.PrimaryImage, "https://static.overfuel.com/") {
		t.Fatalf("primary image = %q", it.PrimaryImage)
	}
	if !strings.Contains(it.URL, "/inventory/used-2017-audi-q7") {
		t.Fatalf("url = %q", it.URL)
	}
}
