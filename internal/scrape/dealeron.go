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
	"strings"
	"time"
)

// DealerOn "Cosmos" SPA sites (markmillersubarusouthtowne.com etc.) serve only
// skeleton cards in the SRP HTML; the inventory comes from a paged JSON API:
//   /api/vhcliaa/vehicle-pages/cosmos/srp/vehicles/<dealerId>/<pageId>?host=<host>&pt=<n>
// The dealerId/pageId pair is published in the page's dealeron_tagging_data
// JSON. Walk every page and synthesize plain cards the generic extractor can
// read.
var dealerOnTaggingRe = regexp.MustCompile(`"dealerId"\s*:\s*"?(\d+)"?\s*,\s*"pageId"\s*:\s*(\d+)`)

type dealerOnSRPResponse struct {
	Paging struct {
		PaginationDataModel struct {
			PageNumber int `json:"PageNumber"`
			TotalPages int `json:"TotalPages"`
			TotalCount int `json:"TotalCount"`
		} `json:"PaginationDataModel"`
	} `json:"Paging"`
	DisplayCards []struct {
		VehicleCard *dealerOnVehicleCard `json:"VehicleCard"`
	} `json:"DisplayCards"`
}

type dealerOnPricingPanel struct {
	PriceStakViewModel struct {
		PriceStakTabsModel struct {
			BuyContent string `json:"BuyContent"`
		} `json:"PriceStakTabsModel"`
	} `json:"PriceStakViewModel"`
}

type dealerOnVehicleCard struct {
	VehicleVin         string  `json:"VehicleVin"`
	VehicleYear        int     `json:"VehicleYear"`
	VehicleMake        string  `json:"VehicleMake"`
	VehicleModel       string  `json:"VehicleModel"`
	VehicleTrim        string  `json:"VehicleTrim"`
	VehicleStockNumber string  `json:"VehicleStockNumber"`
	Mileage            string  `json:"Mileage"`
	VehicleInternetPrice float64 `json:"VehicleInternetPrice"`
	VehicleDetailUrl   string  `json:"VehicleDetailUrl"`
	VehicleCondition   string  `json:"VehicleCondition"`
	VehicleImageModel  struct {
		VehiclePhotoSrc string `json:"VehiclePhotoSrc"`
	} `json:"VehicleImageModel"`
	WasabiVehiclePricingPanelViewModel dealerOnPricingPanel `json:"WasabiVehiclePricingPanelViewModel"`
}

var (
	dealerOnTagRe = regexp.MustCompile(`<[^>]+>`)
	// "$48,967 PROMISE PRICE" / "$21,500 SALE PRICE" — an amount whose label is
	// a price, skipping SAVINGS/DISCOUNT blocks.
	dealerOnAmountLabelRe = regexp.MustCompile(`\$([\d,]+)\s+([A-Z][A-Z ]*PRICE)`)
	// "Promise Price $48,967" / "Retail Price: $52,062" fallback.
	dealerOnLabelAmountRe = regexp.MustCompile(`(?i)([a-z ]*price)\s*:?\s*-?\$([\d,]+)`)
)

// dealerOnPrice digs the selling price out of the pricing stack HTML. New
// vehicles carry VehicleInternetPrice 0 and only publish prices here.
func dealerOnPrice(vc *dealerOnVehicleCard) string {
	buy := vc.WasabiVehiclePricingPanelViewModel.PriceStakViewModel.PriceStakTabsModel.BuyContent
	if strings.TrimSpace(buy) == "" {
		return ""
	}
	txt := strings.Join(strings.Fields(dealerOnTagRe.ReplaceAllString(buy, " ")), " ")
	if m := dealerOnAmountLabelRe.FindStringSubmatch(txt); len(m) == 3 {
		return "$" + m[1]
	}
	best := ""
	for _, m := range dealerOnLabelAmountRe.FindAllStringSubmatch(txt, -1) {
		label := strings.ToLower(m[1])
		if strings.Contains(label, "suggested") || strings.Contains(label, "msrp") {
			if best == "" {
				best = "$" + m[2] // MSRP only as a last resort
			}
			continue
		}
		return "$" + m[2]
	}
	return best
}

func (s Service) expandDealerOnInventory(ctx context.Context, pageURL, renderedHTML string) string {
	if !strings.Contains(renderedHTML, "dealeron_tagging_data") || !strings.Contains(renderedHTML, "vhcliaa") {
		return renderedHTML
	}
	m := dealerOnTaggingRe.FindStringSubmatch(renderedHTML)
	if len(m) < 3 {
		return renderedHTML
	}
	dealerID, pageID := m[1], m[2]
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return renderedHTML
	}
	apiBase := fmt.Sprintf("%s://%s/api/vhcliaa/vehicle-pages/cosmos/srp/vehicles/%s/%s?host=%s",
		u.Scheme, u.Host, dealerID, pageID, u.Host)

	var cards strings.Builder
	cards.WriteString(`<section data-dealeron-api-inventory="true">`)
	seen := map[string]bool{}
	total := 0
	totalPages := 1
	for page := 1; page <= totalPages && page <= 40; page++ {
		if page > 1 {
			// Be polite to the dealer's API — don't hammer pages back-to-back.
			select {
			case <-ctx.Done():
				return renderedHTML
			case <-time.After(400 * time.Millisecond):
			}
		}
		body, ferr := fetchDealerOnJSON(ctx, fmt.Sprintf("%s&pt=%d", apiBase, page))
		if ferr != nil {
			break
		}
		var resp dealerOnSRPResponse
		if jerr := json.Unmarshal([]byte(body), &resp); jerr != nil {
			break
		}
		if resp.Paging.PaginationDataModel.TotalPages > totalPages {
			totalPages = resp.Paging.PaginationDataModel.TotalPages
		}
		added := 0
		for _, dc := range resp.DisplayCards {
			vc := dc.VehicleCard
			if vc == nil || vc.VehicleVin == "" || seen[vc.VehicleVin] {
				continue
			}
			seen[vc.VehicleVin] = true
			title := strings.Join(strings.Fields(fmt.Sprintf("%d %s %s %s",
				vc.VehicleYear, vc.VehicleMake, vc.VehicleModel, vc.VehicleTrim)), " ")
			cards.WriteString(`<div class="dealeron-api-card">`)
			cards.WriteString(`<a class="dealeron-api-url" href="` + html.EscapeString(vc.VehicleDetailUrl) + `"><h3 class="dealeron-api-title">` + html.EscapeString(title) + `</h3></a>`)
			cards.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(vc.VehicleVin) + `">`)
			cards.WriteString(`<span class="dealeron-api-stock">` + html.EscapeString(vc.VehicleStockNumber) + `</span>`)
			if vc.Mileage != "" {
				cards.WriteString(`<span class="dealeron-api-mileage">` + html.EscapeString(vc.Mileage) + `</span>`)
			}
			price := ""
			if vc.VehicleInternetPrice > 0 {
				price = "$" + fmt.Sprintf("%.0f", vc.VehicleInternetPrice)
			} else {
				price = dealerOnPrice(vc)
			}
			if price != "" {
				cards.WriteString(`<span class="dealeron-api-price">` + html.EscapeString(price) + `</span>`)
			}
			if src := strings.TrimSpace(vc.VehicleImageModel.VehiclePhotoSrc); src != "" {
				if strings.HasPrefix(src, "/") {
					src = u.Scheme + "://" + u.Host + src
				}
				cards.WriteString(`<img class="dealeron-api-image" src="` + html.EscapeString(src) + `">`)
			}
			cards.WriteString(`</div>`)
			added++
		}
		total += added
		if added == 0 {
			break
		}
	}
	cards.WriteString(`</section>`)
	if total == 0 {
		return renderedHTML
	}
	return cards.String()
}

// fetchDealerOnJSON hits the Cosmos API with a plain Go client. The API has no
// TLS fingerprinting (unlike the site HTML), so a browser UA is enough — and
// the Python fetch layer must be avoided here: without a cookie it routes JSON
// URLs through a browser, which wraps the body in HTML and breaks parsing.
func fetchDealerOnJSON(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("dealeron api status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return string(b), err
}
