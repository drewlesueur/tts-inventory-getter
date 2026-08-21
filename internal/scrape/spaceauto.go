package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Space Auto powers dealer sites whose SRP is a client-rendered grid: the served
// HTML holds only empty ".vehicle card" skeletons and the real inventory arrives
// from a POST to search-api.space.auto. Scrolling the rendered page is not a way
// out — the grid loads SRP_DEFAULT_PER_PAGE (12) at a time behind an observer the
// headless browser does not trip, so a render yields a dozen cards at most.
//
// Everything needed to call the API is present in the raw HTML, so this runs on
// the plain fetch path too and one request returns the whole inventory.
var (
	spaceAutoIDRe      = regexp.MustCompile(`\$space_id\s*=\s*["']([A-Za-z0-9_-]+)["']`)
	spaceAutoVehJSRe   = regexp.MustCompile(`https?://[^"'\s]+/space/vehicles\.js[^"'\s]*`)
	spaceAutoVehJSONRe = regexp.MustCompile(`var\s+space_vehicles_json\s*=\s*(\[[\s\S]*?\])\s*;`)
)

// spaceAutoSearchURL is a var so tests can point it at a stub server.
var spaceAutoSearchURL = "https://search-api.space.auto/vehicles/search"

// spaceAutoPageLimit is the page size for the search API. The platform's own
// suggestion loader asks for 800 at a time, so this is well inside what it serves.
const spaceAutoPageLimit = 500

// spaceAutoMaxVehicles caps a runaway feed; no dealer SRP legitimately exceeds this.
const spaceAutoMaxVehicles = 5000

type spaceAutoSearchResponse struct {
	Total    int              `json:"total"`
	Vehicles []spaceAutoVehic `json:"vehicles"`
}

type spaceAutoVehic struct {
	VIN                 string   `json:"vin"`
	Stock               string   `json:"stock"`
	Year                int      `json:"year"`
	Make                string   `json:"make"`
	Model               string   `json:"model"`
	Trim                string   `json:"trim"`
	Price               float64  `json:"price"`
	DiscountedPrice     *float64 `json:"discountedPrice"`
	Mileage             float64  `json:"mileage"`
	Color               string   `json:"color"`
	InteriorColor       string   `json:"interiorColor"`
	Body                string   `json:"body"`
	Driveline           string   `json:"driveline"`
	Fuel                string   `json:"fuel"`
	Engine              string   `json:"engine"`
	EngineCylinderCount int      `json:"engineCylinderCount"`
	Transmission        string   `json:"transmission"`
	CityMPG             float64  `json:"cityMPG"`
	HighwayMPG          float64  `json:"highwayMPG"`
	DoorsCount          int      `json:"doorsCount"`
	Condition           string   `json:"condition"`
	Photos              []string `json:"photos"`
}

func expandSpaceAutoInventory(ctx context.Context, pageURL, renderedHTML string) string {
	if !strings.Contains(renderedHTML, "space.auto") && !strings.Contains(renderedHTML, "space-auto.com") {
		return renderedHTML
	}
	if !spaceAutoIDRe.MatchString(renderedHTML) {
		return renderedHTML
	}
	expanded, err := fetchSpaceAutoInventoryHTML(ctx, pageURL, renderedHTML)
	if err != nil {
		return renderedHTML
	}
	return expanded
}

func fetchSpaceAutoInventoryHTML(ctx context.Context, pageURL, renderedHTML string) (string, error) {
	m := spaceAutoIDRe.FindStringSubmatch(renderedHTML)
	if len(m) < 2 {
		return "", fmt.Errorf("space auto dealership id not found")
	}
	dealership := m[1]

	client := &http.Client{Timeout: 30 * time.Second}
	vehicles, err := fetchSpaceAutoVehicles(ctx, client, dealership)
	if err != nil {
		return "", err
	}
	if len(vehicles) == 0 {
		return "", fmt.Errorf("space auto search returned no inventory")
	}
	// The API omits the VDP path, so pair it with the site's own vin->url index.
	vinToURL := fetchSpaceAutoVehicleURLs(ctx, client, pageURL, renderedHTML)

	var cards strings.Builder
	cards.WriteString(`<section data-space-auto-api-inventory="true">`)
	vehicleMaps := make([]map[string]any, 0, len(vehicles))
	for _, v := range vehicles {
		vin := validVINCandidate(v.VIN)
		title := strings.TrimSpace(strings.Join(nonEmptyStrings(
			itoaIfPositive(v.Year), v.Make, v.Model, v.Trim), " "))
		if title == "" {
			continue
		}
		detailURL := vinToURL[strings.ToUpper(v.VIN)]
		if detailURL == "" {
			detailURL = "/vehicle/" + strings.ToLower(v.VIN) + "/"
		}
		price := v.Price
		if v.DiscountedPrice != nil && *v.DiscountedPrice > 0 {
			price = *v.DiscountedPrice
		}

		vm := map[string]any{
			"vin": vin, "stock": v.Stock, "url": detailURL, "title": title,
			"year": v.Year, "make": v.Make, "model": v.Model,
			"price": spaceAutoMoney(price), "mileage": spaceAutoGroup(v.Mileage), "photos": v.Photos,
			"color": v.Color, "body_type": v.Body, "drive_type": v.Driveline,
			"fuel_type": v.Fuel, "engine": v.Engine, "transmission": v.Transmission,
		}
		if v.EngineCylinderCount > 0 {
			vm["cylinders"] = v.EngineCylinderCount
		}
		if v.CityMPG > 0 {
			vm["city_mpg"] = v.CityMPG
		}
		if v.HighwayMPG > 0 {
			vm["highway_mpg"] = v.HighwayMPG
		}
		vehicleMaps = append(vehicleMaps, vm)

		// Mirror the platform's own card markup so a site config written against
		// the live SRP keeps working against the synthesized page.
		cards.WriteString(`<div class="vehicle card">`)
		cards.WriteString(`<a class="vehicle-url" href="` + html.EscapeString(detailURL) + `">`)
		cards.WriteString(`<span class="vehicle-title">` + html.EscapeString(title) + `</span></a>`)
		cards.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(vin) + `">`)
		cards.WriteString(`<span class="vehicle-stock">Stock #: ` + html.EscapeString(v.Stock) + `</span>`)
		if price > 0 {
			cards.WriteString(`<span class="vehicle-price">` + spaceAutoMoney(price) + `</span>`)
		}
		cards.WriteString(`<span class="vehicle-mileage">` + spaceAutoGroup(v.Mileage) + ` mi</span>`)
		if len(v.Photos) > 0 {
			cards.WriteString(`<img class="vehicle-image" src="` + html.EscapeString(v.Photos[0]) + `">`)
		}
		cards.WriteString(`</div>`)
	}
	cards.WriteString(`</section>`)

	nextData, err := json.Marshal(map[string]any{"props": map[string]any{"inventory": vehicleMaps}})
	if err != nil {
		return "", err
	}
	cards.WriteString(`<script id="__NEXT_DATA__" type="application/json">`)
	cards.Write(nextData)
	cards.WriteString(`</script>`)
	return cards.String(), nil
}

// fetchSpaceAutoVehicles walks the search API in pages until the reported total
// is covered. A page that returns nothing new ends the walk so a server that
// ignores skip cannot spin here.
func fetchSpaceAutoVehicles(ctx context.Context, client *http.Client, dealership string) ([]spaceAutoVehic, error) {
	out := make([]spaceAutoVehic, 0, spaceAutoPageLimit)
	for skip := 0; skip < spaceAutoMaxVehicles; skip += spaceAutoPageLimit {
		payload := fmt.Sprintf(
			`{"pagination":{"skip":%d,"limit":%d},"filters":{},"options":{"disableFacets":true},"query":""}`,
			skip, spaceAutoPageLimit)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			spaceAutoSearchURL, bytes.NewBufferString(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("dealership", dealership)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var search spaceAutoSearchResponse
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&search)
		status := resp.StatusCode
		resp.Body.Close()
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("space auto search status %d", status)
		}
		if decErr != nil {
			return nil, decErr
		}
		if len(search.Vehicles) == 0 {
			break
		}
		out = append(out, search.Vehicles...)
		if search.Total > 0 && len(out) >= search.Total {
			break
		}
		if len(search.Vehicles) < spaceAutoPageLimit {
			break
		}
	}
	return out, nil
}

// fetchSpaceAutoVehicleURLs reads the site's static vehicles.js, which indexes
// every VIN to its canonical VDP URL. Best effort: a nil map just means the
// caller falls back to the /vehicle/<vin>/ form.
func fetchSpaceAutoVehicleURLs(ctx context.Context, client *http.Client, pageURL, renderedHTML string) map[string]string {
	jsURL := spaceAutoVehJSRe.FindString(renderedHTML)
	if jsURL == "" {
		return nil
	}
	if !sameHost(pageURL, jsURL) {
		return nil
	}
	if _, err := url.Parse(jsURL); err != nil {
		return nil
	}
	body, err := getIMotorBytes(ctx, client, jsURL)
	if err != nil {
		return nil
	}
	m := spaceAutoVehJSONRe.FindSubmatch(body)
	if len(m) < 2 {
		return nil
	}
	var entries []struct {
		URL   string `json:"url"`
		VIN   string `json:"vin"`
		Stock string `json:"stock"`
	}
	if err := json.Unmarshal(m[1], &entries); err != nil {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.VIN != "" && e.URL != "" {
			out[strings.ToUpper(e.VIN)] = e.URL
		}
	}
	return out
}

// spaceAutoGroup renders a whole number with thousands separators ("62,234").
func spaceAutoGroup(f float64) string {
	if f <= 0 {
		return ""
	}
	digits := strconv.FormatFloat(f, 'f', 0, 64)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func spaceAutoMoney(f float64) string {
	g := spaceAutoGroup(f)
	if g == "" {
		return ""
	}
	return "$" + g
}

func nonEmptyStrings(in ...string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func itoaIfPositive(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
