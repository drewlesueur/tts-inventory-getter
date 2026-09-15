package scrape

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

const santanConfig = "urlkey_dXJsOjp3d3cuc2FudGFubW90b3IuY29tL2NhcnMtZm9yLXNhbGU.yaml"

func loadSantan(t *testing.T) config.SiteConfig {
	t.Helper()
	site, err := (config.Loader{}).LoadByPath(filepath.Join("..", "..", "configs", "sites", santanConfig))
	if err != nil {
		t.Fatalf("load santanmotor config: %v", err)
	}
	return site
}

// CarsForSale classic cards give price and mileage the SAME class and tell them
// apart only by a sibling label, so the platform's [class*='mileage'] selector
// matched nothing and every vehicle came back with no odometer reading.
func TestSantanConfigReadsPriceAndMileageByLabel(t *testing.T) {
	html := `<li class="vehicle-snapshot"><div class="vehicle-snapshot__image">
  <a href="/details/used-2016-cadillac-srx/128898143">
    <img src="https://cdn05.carsforsale.com/abc/480x360/2016-cadillac-srx.jpg"></a></div>
  <h3 class="vehicle-snapshot__title"><a href="/details/used-2016-cadillac-srx/128898143">2016 Cadillac SRX Luxury Collection</a></h3>
  <div class="vehicle-snapshot__main-info-row">
    <div class="vehicle-snapshot__main-info-item">
      <div class="vehicle-snapshot__label">Price</div>
      <div class="vehicle-snapshot__main-info">$12,999</div></div>
    <div class="vehicle-snapshot__main-info-item">
      <div class="vehicle-snapshot__label">Mileage</div>
      <div class="vehicle-snapshot__main-info">118,410</div></div>
  </div></li>`

	items, errs := (DOMExtractor{}).Extract(context.Background(), html, "https://www.santanmotor.com/cars-for-sale", loadSantan(t))
	if len(errs) != 0 || len(items) != 1 {
		t.Fatalf("items=%+v errs=%+v", items, errs)
	}
	if items[0].Price != "$12,999" {
		t.Fatalf("price = %q, want $12,999", items[0].Price)
	}
	if items[0].Mileage != "118,410" {
		t.Fatalf("mileage = %q, want 118,410 (the labelled sibling, not the price)", items[0].Mileage)
	}
}

// The SRP carries no VIN at all, so both VIN and the dealer stock number have to
// come off the VDP — and the stock number there is the dealer's own (5871), not
// the listing id in the URL (128898143).
func TestSantanDetailReadsVINAndDealerStock(t *testing.T) {
	vdp := `<div class="vdp-info-block__info-item"><div class="vehicle-info-icon">
  <svg><title>stock</title></svg></div><div class="vdp-info-block__info-item-text">
  <div class="vdp-info-block__info-item-title">Stock #</div>
  <div class="vdp-info-block__info-item-description">5871</div></div></div>
<div class="vdp-info-block__info-item"><div class="vehicle-info-icon">
  <svg><title>request-VIN</title></svg></div><div class="vdp-info-block__info-item-text">
  <div class="vdp-info-block__info-item-title">VIN</div>
  <div class="vdp-info-block__info-item-description js-vin-message"> 3GYFNBE3XGS540155 </div></div></div>
<div class="vdp-info-block__info-item"><div class="vehicle-info-icon">
  <svg><title>engine</title></svg></div><div class="vdp-info-block__info-item-text">
  <div class="vdp-info-block__info-item-title">Engine</div>
  <div class="vdp-info-block__info-item-description">3.6L V6 308hp 265ft. lbs.</div></div></div>`

	it := model.InventoryItem{URL: "https://www.santanmotor.com/details/used-2016-cadillac-srx/128898143"}
	out, err := populateDetailsFromHTML(context.Background(), nil, it, loadSantan(t), vdp)
	if err != nil {
		t.Fatal(err)
	}
	out = NormalizeItem("https://www.santanmotor.com/cars-for-sale", out)
	if out.VIN != "3GYFNBE3XGS540155" {
		t.Fatalf("vin = %q", out.VIN)
	}
	if out.StockID != "5871" {
		t.Fatalf("stock = %q, want the dealer stock number 5871", out.StockID)
	}
	// The icon's <svg><title>engine</title> and the "Engine" label both used to
	// glue onto the value: "engineEngine3.6L V6 308hp 265ft. lbs.".
	if out.Engine != "3.6L V6 308hp 265ft. lbs." {
		t.Fatalf("engine = %q, want the value with no icon title or label glued on", out.Engine)
	}
}

// The listing id in the URL is synthesized into StockID by NormalizeItem before
// the detail sweep runs, and every stock sweep is guarded on StockID being
// empty — so the dealer's real stock number could never replace it, and
// santanmotor cached 128898143 for a car whose VDP says 5871.
func TestSantanDetailOverridesSynthesizedListingID(t *testing.T) {
	vdp := `<div class="vdp-info-block__info-item-text">
  <div class="vdp-info-block__info-item-title">Stock #</div>
  <div class="vdp-info-block__info-item-description">5871</div></div>`
	it := model.InventoryItem{
		URL:     "https://www.santanmotor.com/details/used-2016-cadillac-srx/128898143",
		StockID: "128898143", // what NormalizeItem synthesized from the URL
		VIN:     "3GYFNBE3XGS540155",
	}
	out, err := populateDetailsFromHTML(context.Background(), nil, it, loadSantan(t), vdp)
	if err != nil {
		t.Fatal(err)
	}
	if out.StockID != "5871" {
		t.Fatalf("stock = %q, want the VDP's 5871 rather than the listing id", out.StockID)
	}
}

// ...but when the detail page has no stock number either, clearing it must not
// strand the item without an identifier: NormalizeItem re-fills it, preferring
// the VIN as the stable cross-system id and only using the listing id when
// there is no valid VIN.
func TestSantanDetailFallsBackWhenVDPHasNoStock(t *testing.T) {
	it := model.InventoryItem{
		URL:     "https://www.santanmotor.com/details/used-2016-cadillac-srx/128898143",
		StockID: "128898143",
		VIN:     "3GYFNBE3XGS540155",
	}
	out, err := populateDetailsFromHTML(context.Background(), nil, it, loadSantan(t), `<div>no specs here</div>`)
	if err != nil {
		t.Fatal(err)
	}
	if out.StockID != "3GYFNBE3XGS540155" {
		t.Fatalf("stock = %q, want the VIN fallback", out.StockID)
	}

	// With no VIN either, the listing id is the last resort.
	noVIN := model.InventoryItem{URL: "https://www.santanmotor.com/details/used-2016-cadillac-srx/128898143", StockID: "128898143"}
	out, err = populateDetailsFromHTML(context.Background(), nil, noVIN, loadSantan(t), `<div>no specs here</div>`)
	if err != nil {
		t.Fatal(err)
	}
	if out.StockID != "128898143" {
		t.Fatalf("stock = %q, want the listing id as the last resort", out.StockID)
	}
}
