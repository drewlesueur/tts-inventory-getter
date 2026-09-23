package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Drive N-Motion runs three stores off one Next.js storefront, and
// /inventory returns all of them (412 vehicles: Thornton 154, Greeley 161,
// Rio Rancho 97). Splitting them per TapToSign account is awkward because the
// two obvious approaches each fail on their own:
//
//   - ?dealer_id[]=183 filters correctly, but CacheKeyForSourceURL drops the
//     query string, so all three stores collapse onto the single cache key
//     url::www.drivenmotion.com/inventory and would overwrite each other.
//   - /inventory/thornton yields a distinct cache key, but the Next.js
//     catch-all route ignores the segment and serves all 412 unfiltered.
//
// So the registered URL is the path form (one key per store) and this fetcher
// translates it into the filtered query the site actually honours, walking
// every page of that store and returning one combined document.
var drivenMotionDealers = map[string]struct {
	ID   int
	Name string
}{
	"thornton":   {183, "Drive N-Motion Thornton"},
	"greeley":    {184, "Drive N-Motion Greeley"},
	"rio-rancho": {185, "Drive N-Motion Rio Rancho"},
}

type drivenMotionVehicle struct {
	VIN         string   `json:"vin"`
	StockNumber string   `json:"stocknumber"`
	Year        int      `json:"year"`
	Make        string   `json:"make"`
	Model       string   `json:"model"`
	Trim        string   `json:"trim"`
	Price       float64  `json:"price"`
	Mileage     float64  `json:"mileage"`
	Status      string   `json:"status"`
	Wholesale   int      `json:"wholesale"`
	DealerID    int      `json:"dealer_id"`
	URL         string   `json:"url"`
	Photos      []string `json:"photos"`
	ExtColor    string   `json:"exteriorcolor"`
	Body        string   `json:"body"`
	Fuel        string   `json:"fuel"`
	Drivetrain  string   `json:"drivetrainstandard"`
}

type drivenMotionPayload struct {
	Props struct {
		PageProps struct {
			Inventory struct {
				Meta struct {
					Total int `json:"total"`
				} `json:"meta"`
				Results []drivenMotionVehicle `json:"results"`
			} `json:"inventory"`
		} `json:"pageProps"`
	} `json:"props"`
}

// drivenMotionStoreFor returns the store a /inventory/<slug> URL refers to.
func drivenMotionStoreFor(pageURL string) (int, string, bool) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return 0, "", false
	}
	slug := strings.ToLower(strings.Trim(strings.TrimPrefix(strings.TrimSuffix(u.Path, "/"), "/inventory"), "/"))
	if slug == "" {
		return 0, "", false
	}
	d, ok := drivenMotionDealers[slug]
	if !ok {
		return 0, "", false
	}
	return d.ID, d.Name, true
}

func fetchDrivenMotionInventoryHTML(ctx context.Context, pageURL string) (string, error) {
	dealerID, dealerName, ok := drivenMotionStoreFor(pageURL)
	if !ok {
		return "", fmt.Errorf("drivenmotion: %q names no known store", pageURL)
	}
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("drivenmotion: invalid inventory URL")
	}
	origin := u.Scheme + "://" + u.Host
	client := &http.Client{Timeout: 45 * time.Second}

	var all []drivenMotionVehicle
	seen := make(map[string]bool)
	for page := 1; page <= 60; page++ {
		// dealer_id[] is the form the site honours — a bare dealer_id=183 is
		// silently ignored and returns every store.
		target := fmt.Sprintf("%s/inventory?dealer_id%%5B%%5D=%d&page=%d", origin, dealerID, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if readErr != nil {
			return "", readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("drivenmotion: page %d status %d", page, resp.StatusCode)
		}
		payload, err := parseDrivenMotionPayload(string(body))
		if err != nil {
			return "", err
		}
		fresh := 0
		for _, v := range payload.Props.PageProps.Inventory.Results {
			// Belt and braces: the filter should make this redundant, but never
			// let another store's stock into a store-specific feed.
			if v.DealerID != 0 && v.DealerID != dealerID {
				continue
			}
			if v.Status != "" && !strings.EqualFold(v.Status, "active") {
				continue
			}
			if v.Wholesale != 0 {
				continue
			}
			id := strings.ToUpper(strings.TrimSpace(v.VIN))
			if id == "" {
				id = strings.ToUpper(strings.TrimSpace(v.StockNumber))
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			all = append(all, v)
			fresh++
		}
		total := payload.Props.PageProps.Inventory.Meta.Total
		if fresh == 0 || len(payload.Props.PageProps.Inventory.Results) == 0 || (total > 0 && len(all) >= total) {
			break
		}
	}
	if len(all) == 0 {
		return "", fmt.Errorf("drivenmotion: no vehicles for %s", dealerName)
	}
	return renderDrivenMotionCards(origin, all), nil
}

func parseDrivenMotionPayload(body string) (*drivenMotionPayload, error) {
	const marker = `<script id="__NEXT_DATA__" type="application/json">`
	start := strings.Index(body, marker)
	if start == -1 {
		return nil, fmt.Errorf("drivenmotion: no __NEXT_DATA__ block")
	}
	start += len(marker)
	end := strings.Index(body[start:], `</script>`)
	if end == -1 {
		return nil, fmt.Errorf("drivenmotion: unterminated __NEXT_DATA__ block")
	}
	var p drivenMotionPayload
	if err := json.Unmarshal([]byte(body[start:start+end]), &p); err != nil {
		return nil, fmt.Errorf("drivenmotion: decode __NEXT_DATA__: %w", err)
	}
	return &p, nil
}

func renderDrivenMotionCards(origin string, vehicles []drivenMotionVehicle) string {
	var b strings.Builder
	b.WriteString(`<section data-drivenmotion-store-inventory="true">`)
	maps := make([]map[string]any, 0, len(vehicles))
	for _, v := range vehicles {
		title := strings.TrimSpace(fmt.Sprintf("%d %s %s %s", v.Year, v.Make, v.Model, v.Trim))
		detail := v.URL
		if strings.HasPrefix(detail, "/") {
			detail = origin + detail
		}
		images := make([]string, 0, len(v.Photos))
		for _, p := range v.Photos {
			if p != "" {
				images = append(images, p)
			}
		}
		maps = append(maps, map[string]any{
			"stockid": v.StockNumber, "url": detail, "title": title,
			"year": v.Year, "make": v.Make, "model": v.Model, "trim": v.Trim,
			"price": v.Price, "mileage": v.Mileage, "vin": validVINCandidate(v.VIN),
			"exterior_color": v.ExtColor, "body_type": v.Body, "fuel_type": v.Fuel,
			"drivetrain": v.Drivetrain, "images": images,
		})
		b.WriteString(`<div class="srp-card">`)
		b.WriteString(`<a href="` + html.EscapeString(detail) + `"><h2>` + html.EscapeString(title) + `</h2></a>`)
		b.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(validVINCandidate(v.VIN)) + `">`)
		b.WriteString(`<span class="dm-stock">` + html.EscapeString(v.StockNumber) + `</span>`)
		b.WriteString(`<span class="price">$` + strconv.FormatFloat(v.Price, 'f', 0, 64) + `</span>`)
		// Bare number: the extractor appends the "mi" unit itself.
		b.WriteString(`<span class="mileage">` + strconv.FormatFloat(v.Mileage, 'f', 0, 64) + `</span>`)
		if len(images) > 0 {
			b.WriteString(`<img src="` + html.EscapeString(images[0]) + `">`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</section>`)
	nextData, _ := json.Marshal(map[string]any{"props": map[string]any{"inventory": maps}})
	b.WriteString(`<script id="__NEXT_DATA__" type="application/json">`)
	b.Write(nextData)
	b.WriteString(`</script>`)
	return b.String()
}
