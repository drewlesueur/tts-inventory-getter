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

// DealerSync SRPs server-render only the first page of vehicles and load the
// rest by infinite scroll, so a plain fetch sees a fraction of the lot
// (dnkselect.com served 15 of 76). The scroll calls a JSON endpoint:
//
//	GET /Inventory/Search?Results=15&startIndex=N&SortCriteria=Year&SortDirection=desc&version=2
//
// which reports totalResults and returns 15 vehicles per call whatever Results
// says. Walk it and synthesize cards the generic extractor can read.
const (
	dealerSyncPageSize = 15
	dealerSyncMaxPages = 60
)

type dealerSyncSearchResponse struct {
	Success      bool                `json:"Success"`
	StartIndex   int                 `json:"startIndex"`
	TotalResults int                 `json:"totalResults"`
	Vehicles     []dealerSyncVehicle `json:"vehicles"`
}

type dealerSyncVehicle struct {
	Vin              string  `json:"Vin"`
	StockNo          string  `json:"StockNo"`
	VehicleTitle     string  `json:"VehicleTitle"`
	VehicleName      string  `json:"VehicleName"`
	// Year is a *string* in this API ("2025"). Typing it as int makes the whole
	// response fail to decode, and the expander then silently falls back to the
	// 15-vehicle server-rendered page.
	Year             string  `json:"Year"`
	Make             string  `json:"Make"`
	Model            string  `json:"Model"`
	Trim             string  `json:"Trim"`
	Mileage          int     `json:"Mileage"`
	FinalPrice       float64 `json:"FinalPrice"`
	InternetPrice    float64 `json:"InternetPrice"`
	VehicleDetailURL string  `json:"VehicleDetailUrl"`
	FirstImageURL    string  `json:"FirstImageUrl"`
}

func (s Service) expandDealerSyncInventory(ctx context.Context, pageURL, renderedHTML string) string {
	if !strings.Contains(renderedHTML, "ds-vehicle-list-item") && !strings.Contains(renderedHTML, "images.dealersync.com") {
		return renderedHTML
	}
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return renderedHTML
	}
	endpoint := u.Scheme + "://" + u.Host + "/Inventory/Search"

	var cards strings.Builder
	cards.WriteString(`<section data-dealersync-api-inventory="true">`)
	seen := map[string]bool{}
	total := 0
	for page := 0; page < dealerSyncMaxPages; page++ {
		if page > 0 {
			// Be polite to the dealer's API — don't hammer pages back-to-back.
			select {
			case <-ctx.Done():
				return renderedHTML
			case <-time.After(400 * time.Millisecond):
			}
		}
		q := url.Values{}
		q.Set("Results", strconv.Itoa(dealerSyncPageSize))
		q.Set("startIndex", strconv.Itoa(page*dealerSyncPageSize))
		q.Set("SortCriteria", "Year")
		q.Set("SortDirection", "desc")
		q.Set("version", "2")
		q.Set("IsCertified", "-1")
		q.Set("IsFuzzySearch", "false")

		body, ferr := fetchDealerSyncJSON(ctx, endpoint+"?"+q.Encode())
		if ferr != nil {
			break
		}
		var resp dealerSyncSearchResponse
		if jerr := json.Unmarshal([]byte(body), &resp); jerr != nil || !resp.Success {
			break
		}
		added := 0
		for _, v := range resp.Vehicles {
			key := strings.ToUpper(strings.TrimSpace(v.Vin))
			if key == "" {
				key = strings.ToUpper(strings.TrimSpace(v.StockNo))
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			cards.WriteString(dealerSyncCardHTML(u, v))
			added++
		}
		total += added
		if added == 0 || (resp.TotalResults > 0 && total >= resp.TotalResults) {
			break
		}
	}
	cards.WriteString(`</section>`)
	if total == 0 {
		return renderedHTML
	}
	return cards.String()
}

func dealerSyncCardHTML(base *url.URL, v dealerSyncVehicle) string {
	title := strings.TrimSpace(v.VehicleTitle)
	if title == "" {
		title = strings.TrimSpace(v.VehicleName)
	}
	if title == "" {
		title = strings.Join(nonEmptyStrings(v.Year, v.Make, v.Model, v.Trim), " ")
	}

	detail := strings.TrimSpace(v.VehicleDetailURL)
	if strings.HasPrefix(detail, "/") {
		detail = base.Scheme + "://" + base.Host + detail
	}
	img := strings.TrimSpace(v.FirstImageURL)
	if strings.HasPrefix(img, "//") {
		img = base.Scheme + ":" + img
	}
	// FinalPrice includes the add-ons (doc fee) the SRP shows as "Sale Price";
	// InternetPrice is the pre-fee figure and is the fallback.
	price := v.FinalPrice
	if price <= 0 {
		price = v.InternetPrice
	}

	var b strings.Builder
	b.WriteString(`<div class="ds-api-card">`)
	b.WriteString(`<a class="ds-api-url" href="` + html.EscapeString(detail) + `">`)
	b.WriteString(`<h2 class="ds-api-title">` + html.EscapeString(title) + `</h2></a>`)
	b.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(strings.TrimSpace(v.Vin)) + `">`)
	b.WriteString(`<span class="ds-api-stock">` + html.EscapeString(strings.TrimSpace(v.StockNo)) + `</span>`)
	if v.Mileage > 0 {
		b.WriteString(`<span class="ds-api-mileage">` + strconv.Itoa(v.Mileage) + `</span>`)
	}
	if price > 0 {
		b.WriteString(`<span class="ds-api-price">$` + fmt.Sprintf("%.0f", price) + `</span>`)
	}
	if img != "" {
		b.WriteString(`<img class="ds-api-image" src="` + html.EscapeString(img) + `">`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// fetchDealerSyncJSON uses a plain Go client: the JSON endpoint has no bot
// check, and routing a JSON URL through the Python/browser layer would wrap the
// body in HTML and break parsing.
func fetchDealerSyncJSON(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dealersync search status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return string(b), err
}
