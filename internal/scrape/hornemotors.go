package scrape

import (
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

// Horne Motors runs a Vite/React SPA: /inventory ships a 2 KB shell with an
// empty <div id="root">, and the browser needs ~30s to hydrate before a single
// card exists. Behind it sits a plain JSON endpoint that answers over ordinary
// HTTP from any IP, returns the whole lot in one call and is already filtered
// to available stock, so we skip the browser entirely.
//
//	GET /api/inventory?status=available&sortBy=price_asc&limit=500
//
// status=available matters: it is the site's own sold filter, the same trap as
// the CarsForSale SoldStatus dropdown that cached 77 sold cars for snbmotors.

type horneMotorsPhoto struct {
	URL       string `json:"url"`
	IsPrimary bool   `json:"isPrimary"`
	SortOrder int    `json:"sortOrder"`
}

type horneMotorsVehicle struct {
	ID           int                `json:"id"`
	VIN          string             `json:"vin"`
	StockNumber  string             `json:"stockNumber"`
	Year         int                `json:"year"`
	Make         string             `json:"make"`
	Model        string             `json:"model"`
	Trim         string             `json:"trim"`
	HornePrice   float64            `json:"hornePrice"`
	Mileage      float64            `json:"mileage"`
	Status       string             `json:"status"`
	ExtColor     string             `json:"exteriorColor"`
	Drivetrain   string             `json:"drivetrain"`
	Transmission string             `json:"transmission"`
	Engine       string             `json:"engineDescription"`
	BodyClass    string             `json:"bodyClass"`
	FuelType     string             `json:"fuelType"`
	Photos       []horneMotorsPhoto `json:"photos"`
}

type horneMotorsResponse struct {
	Vehicles []horneMotorsVehicle `json:"vehicles"`
	Total    int                  `json:"total"`
}

var horneMotorsSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugHorneMotors(s string) string {
	return strings.Trim(horneMotorsSlugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

func fetchHorneMotorsInventoryHTML(ctx context.Context, pageURL string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid Horne Motors inventory URL")
	}
	origin := u.Scheme + "://" + u.Host
	apiURL := origin + "/api/inventory?status=available&sortBy=price_asc&limit=500"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	// The bare SPA document 403s for non-browser agents; the API does not, but
	// send a browser UA anyway so we look like the page's own fetch.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Horne Motors inventory API status %d", resp.StatusCode)
	}

	var payload horneMotorsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("Horne Motors inventory API decode: %w", err)
	}
	if len(payload.Vehicles) == 0 {
		return "", fmt.Errorf("Horne Motors inventory API returned no vehicles")
	}

	var cards strings.Builder
	cards.WriteString(`<section data-hornemotors-api-inventory="true">`)
	vehicleMaps := make([]map[string]any, 0, len(payload.Vehicles))
	for _, v := range payload.Vehicles {
		// Defensive: the query already filters to available, but never emit a
		// sold car if the API ever ignores the parameter.
		if v.Status != "" && !strings.EqualFold(v.Status, "available") {
			continue
		}
		title := strings.TrimSpace(fmt.Sprintf("%d %s %s %s", v.Year, v.Make, v.Model, v.Trim))

		// Live VDP path, e.g. /inventory/2007-hyundai-santa-fe-se-6182
		slugParts := []string{strconv.Itoa(v.Year), v.Make, v.Model, v.Trim}
		slug := make([]string, 0, len(slugParts))
		for _, p := range slugParts {
			if s := slugHorneMotors(p); s != "" {
				slug = append(slug, s)
			}
		}
		detailURL := "/inventory/" + strings.Join(slug, "-")
		if v.StockNumber != "" {
			detailURL += "-" + slugHorneMotors(v.StockNumber)
		}

		images := make([]string, 0, len(v.Photos))
		for _, p := range v.Photos {
			if p.URL == "" {
				continue
			}
			img := p.URL
			if strings.HasPrefix(img, "/") {
				img = origin + img
			}
			images = append(images, img)
		}

		vehicleMaps = append(vehicleMaps, map[string]any{
			"stockid": v.StockNumber, "url": detailURL, "title": title,
			"year": v.Year, "make": v.Make, "model": v.Model, "trim": v.Trim,
			"price": v.HornePrice, "mileage": v.Mileage, "vin": validVINCandidate(v.VIN),
			"exterior_color": v.ExtColor, "drivetrain": v.Drivetrain,
			"transmission": v.Transmission, "engine": v.Engine,
			"body_type": v.BodyClass, "fuel_type": v.FuelType,
			"images": images,
		})

		cards.WriteString(`<div class="hm-vehicle-card">`)
		cards.WriteString(`<a href="` + html.EscapeString(detailURL) + `"><h3>` + html.EscapeString(title) + `</h3></a>`)
		cards.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(validVINCandidate(v.VIN)) + `">`)
		cards.WriteString(`<span class="hm-stock">` + html.EscapeString(v.StockNumber) + `</span>`)
		cards.WriteString(`<span class="hm-price">$` + strconv.FormatFloat(v.HornePrice, 'f', 0, 64) + `</span>`)
		// Bare number: the extractor appends the "mi" unit itself, so emitting
		// it here too yields "210883 mi mi".
		cards.WriteString(`<span class="hm-mileage">` + strconv.FormatFloat(v.Mileage, 'f', 0, 64) + `</span>`)
		if len(images) > 0 {
			cards.WriteString(`<img src="` + html.EscapeString(images[0]) + `">`)
		}
		cards.WriteString(`</div>`)
	}
	cards.WriteString(`</section>`)

	nextData, _ := json.Marshal(map[string]any{"props": map[string]any{"inventory": vehicleMaps}})
	cards.WriteString(`<script id="__NEXT_DATA__" type="application/json">`)
	cards.Write(nextData)
	cards.WriteString(`</script>`)
	return cards.String(), nil
}
