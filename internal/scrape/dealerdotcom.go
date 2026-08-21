package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Dealer.com (DDC) sites render their SRP client-side, but every hydrated page
// embeds the widget's own view model in a script:
//
//	DDC.WS.state['ws-inv-data']['inventory-data-busN'] = {"WIS":{"pageInfo":…,"inventory":[…]}}
//
// That blob carries the full record — VIN, odometer, pricing, images, engine,
// colors — none of which reaches the card markup, so scraping the DOM yields
// listings with no VIN and no detail. It also carries totalCount/pageSize, the
// only reliable way to page: the site's own pagination links are elided with an
// ellipsis, and looksLikePaginationOrInventoryURL discards "?start=N" anyway.
//
// Parsing is best effort. Hydration is racy, and when the blob is absent the
// original HTML is returned so the configured DOM selectors still apply.
var (
	ddcStateRe = regexp.MustCompile(`DDC\.WS\.state\[['"]ws-inv-data['"]\]\[['"][^'"]+['"]\]\s*=\s*`)
	ddcMetaRe  = regexp.MustCompile(`data-ddc-total="(\d+)"\s+data-ddc-page-size="(\d+)"\s+data-ddc-page-start="(\d+)"`)
	ddcMilesRe = regexp.MustCompile(`(?i)\s*(?:miles|mi)\.?\s*$`)
)

// ddcMaxPages bounds the synthesized page walk regardless of a bogus totalCount.
const ddcMaxPages = 60

type ddcStateBlob struct {
	WIS struct {
		PageInfo struct {
			TotalCount int `json:"totalCount"`
			PageSize   int `json:"pageSize"`
			PageStart  int `json:"pageStart"`
		} `json:"pageInfo"`
		Inventory []ddcVehicle `json:"inventory"`
	} `json:"WIS"`
}

type ddcVehicle struct {
	VIN         string   `json:"vin"`
	StockNumber string   `json:"stockNumber"`
	Year        int      `json:"year"`
	Make        string   `json:"make"`
	Model       string   `json:"model"`
	Trim        string   `json:"trim"`
	Title       []string `json:"title"`
	Link        string   `json:"link"`
	BodyStyle   string   `json:"bodyStyle"`
	FuelType    string   `json:"fuelType"`
	Condition   string   `json:"condition"`
	Certified   bool     `json:"certified"`
	Images      []struct {
		URI string `json:"uri"`
	} `json:"images"`
	Pricing struct {
		RetailPrice string `json:"retailPrice"`
		DPrice      []struct {
			Value        string `json:"value"`
			IsFinalPrice bool   `json:"isFinalPrice"`
		} `json:"dprice"`
	} `json:"pricing"`
	Attributes          []ddcAttribute `json:"attributes"`
	TrackingAttributes  []ddcAttribute `json:"trackingAttributes"`
	HighlightAttributes []ddcAttribute `json:"highlightedAttributes"`
}

type ddcAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func ddcAttr(lists [][]ddcAttribute, names ...string) string {
	for _, name := range names {
		for _, list := range lists {
			for _, a := range list {
				if strings.EqualFold(a.Name, name) && strings.TrimSpace(a.Value) != "" {
					return strings.TrimSpace(a.Value)
				}
			}
		}
	}
	return ""
}

func expandDealerDotComInventory(_ context.Context, pageURL, renderedHTML string) string {
	loc := ddcStateRe.FindStringIndex(renderedHTML)
	if loc == nil {
		return renderedHTML
	}
	raw := jsonObjectAt(renderedHTML, loc[1])
	if raw == "" {
		return renderedHTML
	}
	var blob ddcStateBlob
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		return renderedHTML
	}
	if len(blob.WIS.Inventory) == 0 {
		return renderedHTML
	}
	return buildDealerDotComHTML(pageURL, blob)
}

func buildDealerDotComHTML(pageURL string, blob ddcStateBlob) string {
	info := blob.WIS.PageInfo
	var b strings.Builder
	b.WriteString(`<section data-ddc-api-inventory="true" `)
	b.WriteString(fmt.Sprintf(`data-ddc-total="%d" data-ddc-page-size="%d" data-ddc-page-start="%d">`,
		info.TotalCount, info.PageSize, info.PageStart))
	// Phrased so inventoryTotalTextRe recognizes the run, which caps the walk.
	if info.TotalCount > 0 {
		b.WriteString(fmt.Sprintf(`<span class="ddc-result-count">%d - %d of %d vehicles</span>`,
			info.PageStart+1, info.PageStart+len(blob.WIS.Inventory), info.TotalCount))
	}

	b.WriteString(`<ul class="inventory-listing list-unstyled">`)
	vehicleMaps := make([]map[string]any, 0, len(blob.WIS.Inventory))
	for _, v := range blob.WIS.Inventory {
		attrs := [][]ddcAttribute{v.Attributes, v.HighlightAttributes, v.TrackingAttributes}

		title := strings.TrimSpace(strings.Join(v.Title, " "))
		if title == "" {
			title = strings.Join(nonEmptyStrings(itoaIfPositive(v.Year), v.Make, v.Model, v.Trim), " ")
		}
		if title == "" {
			continue
		}
		price := strings.TrimSpace(v.Pricing.RetailPrice)
		for _, d := range v.Pricing.DPrice {
			if d.IsFinalPrice && strings.TrimSpace(d.Value) != "" {
				price = strings.TrimSpace(d.Value)
				break
			}
		}
		// "5,227 miles" -> "5,227", matching the bare figure other sites yield.
		mileage := ddcMilesRe.ReplaceAllString(ddcAttr(attrs, "odometer"), "")
		photos := make([]string, 0, len(v.Images))
		for _, im := range v.Images {
			if strings.TrimSpace(im.URI) != "" {
				photos = append(photos, im.URI)
			}
		}

		vm := map[string]any{
			"vin": validVINCandidate(v.VIN), "stock": v.StockNumber, "url": v.Link,
			"title": title, "year": v.Year, "make": v.Make, "model": v.Model,
			"price": price, "mileage": mileage, "photos": photos,
			"body_type": v.BodyStyle, "fuel_type": v.FuelType,
		}
		putIfSet(vm, "color", ddcAttr(attrs, "exteriorColor"))
		putIfSet(vm, "engine", ddcAttr(attrs, "engine"))
		putIfSet(vm, "transmission", ddcAttr(attrs, "transmission"))
		putIfSet(vm, "drive_type", ddcAttr(attrs, "normalDriveLine", "driveLine"))
		putIfSet(vm, "city_mpg", ddcAttr(attrs, "cityFuelEconomy"))
		putIfSet(vm, "highway_mpg", ddcAttr(attrs, "highwayFuelEconomy"))
		vehicleMaps = append(vehicleMaps, vm)

		// Mirror DDC's own card classes so a config written against the live SRP
		// keeps working, and so countCards sees the real number of listings.
		b.WriteString(`<li class="box box-border vehicle-card vehicle-card-detailed">`)
		b.WriteString(`<h2 class="vehicle-card-title"><a href="` + html.EscapeString(v.Link) + `">`)
		b.WriteString(`<span>` + html.EscapeString(title) + `</span></a></h2>`)
		b.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(validVINCandidate(v.VIN)) + `">`)
		if price != "" {
			b.WriteString(`<span class="price-value">` + html.EscapeString(price) + `</span>`)
		}
		if mileage != "" {
			b.WriteString(`<span class="ddc-odometer">` + html.EscapeString(mileage) + `</span>`)
		}
		b.WriteString(`<span class="stockNumber">Stock # ` + html.EscapeString(v.StockNumber) + `</span>`)
		if len(photos) > 0 {
			b.WriteString(`<img class="ddc-photo" src="` + html.EscapeString(photos[0]) + `">`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul></section>`)

	nextData, err := json.Marshal(map[string]any{"props": map[string]any{"inventory": vehicleMaps}})
	if err != nil {
		return ""
	}
	b.WriteString(`<script id="__NEXT_DATA__" type="application/json">`)
	b.Write(nextData)
	b.WriteString(`</script>`)
	_ = pageURL
	return b.String()
}

// extractDealerDotComPageURLs synthesizes every remaining ?start=N offset from
// the totals stamped onto the expanded page. Emitting all of them at once keeps
// one flaky render from truncating the walk, matching the other platform helpers.
func extractDealerDotComPageURLs(pageURL, pageHTML string) []string {
	m := ddcMetaRe.FindStringSubmatch(pageHTML)
	if len(m) < 4 {
		return nil
	}
	total := parsePositiveInt(m[1])
	size := parsePositiveInt(m[2])
	if total <= 0 || size <= 0 || total <= size {
		return nil
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil
	}
	pages := (total + size - 1) / size
	if pages > ddcMaxPages {
		pages = ddcMaxPages
	}
	out := make([]string, 0, pages)
	for p := 1; p < pages; p++ {
		next := *u
		q := next.Query()
		q.Set("start", strconv.Itoa(p*size))
		next.RawQuery = q.Encode()
		out = append(out, next.String())
	}
	return out
}

func putIfSet(m map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		m[key] = value
	}
}

// jsonObjectAt returns the brace-balanced object starting at start, ignoring
// braces inside string literals. Empty if the object never closes.
func jsonObjectAt(s string, start int) string {
	if start >= len(s) || s[start] != '{' {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
