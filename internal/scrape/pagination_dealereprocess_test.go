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

// Guards the autosensenh config's selectors against the shape of a real card:
// stock and mileage are labelled table rows, the buyer's price is the *last*
// of two <dd class="vehicle_price">, the VIN rides on a chat widget's data-vin,
// and the photo is lazy-loaded on data-src next to a 360-video play button.
func TestAutosenseConfigExtractsFullCard(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "sites",
		"urlkey_dXJsOjp3d3cuYXV0b3NlbnNlbmguY29tL3NlYXJjaC91c2VkLWNoaWNoZXN0ZXItbmg.yaml")
	site, err := (config.Loader{}).LoadByPath(path)
	if err != nil {
		t.Fatalf("load autosensenh config: %v", err)
	}
	html := `<div class="vehicle_item" data-vehicle_id="122961395">
  <h2 class="vehicle_title"><a href="/auto/used-2013-mazda-mazda3-i-sv-chichester-nh/122961395/">Used 2013 Mazda Mazda3 i SV</a></h2>
  <img src="//gcbimages.storage.googleapis.com/vidbtn/play_video_360.png" alt="Button for Video">
  <img class="lazyload-target loopslider__image" data-src="https://cloudflareimages.dealereprocess.com/resrc/images/c_limit/v1/dvp/3886/54603238245/Used-2013-Mazda-Mazda3-iSV-ID54603238245-aHR0cDovL2V4YW1wbGU=">
  <div class="simpwebchat_srp_item" data-vin="JM1BL1TG1D1779942" data-stock_no="N1484A"></div>
  <dl><dt>Price</dt><dd class="vehicle_price">$4,000</dd>
      <dt>Transparent Price includes Dealer Admin Fee</dt><dd class="vehicle_price">$4,798</dd></dl>
  <table class="srp_details"><tbody>
    <tr><td class="details-overview_title bold">Mileage</td><td class="details-overview_data">188,127 </td></tr>
    <tr><td class="details-overview_title bold">Stock #</td><td class="details-overview_data">N1484A</td></tr>
    <tr><td class="details-overview_title bold">VIN</td><td class="details-overview_data">JM1BL1TG1D1779942</td></tr>
  </tbody></table>
</div>` + eProcessPager

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, site.BaseURL, site)
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("expected one vehicle, got items=%+v errors=%+v", items, errs)
	}
	it := items[0]
	if it.Title != "Used 2013 Mazda Mazda3 i SV" || it.StockID != "N1484A" || it.VIN != "JM1BL1TG1D1779942" {
		t.Fatalf("unexpected identity fields: %+v", it)
	}
	if it.Price != "$4,798" || it.Mileage != "188,127" {
		t.Fatalf("price/mileage = %q/%q, want the post-fee price and the odometer", it.Price, it.Mileage)
	}
	if !strings.HasPrefix(it.PrimaryImage, "https://cloudflareimages.dealereprocess.com/") {
		t.Fatalf("primary image = %q, want the vehicle photo, not the video button", it.PrimaryImage)
	}
	if it.Year != "2013" || it.Make != "Mazda" || it.Model != "Mazda3" {
		t.Fatalf("year/make/model = %q/%q/%q", it.Year, it.Make, it.Model)
	}
	// Every field is on the card, so the per-item gate must skip the VDP fetch —
	// each one would cost a Cloudflare-guarded browser render.
	if !detailFetchWouldAddNothing(it) {
		t.Fatal("expected the card to be complete enough to skip the detail fetch")
	}
	next := extractNextPageURLs(site.BaseURL, html, site)
	if len(next) != 6 || !strings.HasSuffix(next[0], "?p=2") {
		t.Fatalf("next pages = %v", next)
	}
}
