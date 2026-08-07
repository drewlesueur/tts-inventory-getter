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

var imotorDealerIDRe = regexp.MustCompile(`"dealerId"\s*:\s*"?([0-9]+)"?`)

type imotorSearchResponse struct {
	Results []struct {
		Hits []imotorHit `json:"hits"`
	} `json:"results"`
}

type imotorHit struct {
	ID          int      `json:"id"`
	VehicleType string   `json:"vehType"`
	Make        string   `json:"make"`
	Model       string   `json:"model"`
	Year        int      `json:"year"`
	Price       float64  `json:"price"`
	Odometer    float64  `json:"odometer"`
	VIN         string   `json:"vin"`
	Images      []string `json:"images"`
}

func expandIMotorInventory(ctx context.Context, pageURL, renderedHTML string) string {
	if !strings.Contains(renderedHTML, `data-imotor-site`) {
		return renderedHTML
	}
	expanded, err := fetchIMotorInventoryHTML(ctx, pageURL)
	if err != nil {
		return renderedHTML
	}
	return expanded
}

func fetchIMotorInventoryHTML(ctx context.Context, pageURL string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid iMotor inventory URL")
	}
	pageDataURL := u.Scheme + "://" + u.Host + "/page-data" + strings.TrimSuffix(u.Path, "/") + "/page-data.json"
	client := &http.Client{Timeout: 20 * time.Second}
	pageData, err := getIMotorBytes(ctx, client, pageDataURL)
	if err != nil {
		return "", err
	}
	match := imotorDealerIDRe.FindSubmatch(pageData)
	if len(match) < 2 {
		return "", fmt.Errorf("iMotor dealer ID not found")
	}
	payload := fmt.Sprintf(`{"queries":[{"indexUid":"dealer_stock_%s","q":"","filter":["NOT (adType = \"for_rent\" OR adType = 3)"],"limit":500,"offset":0,"sort":["createdAt:asc"]}]}`, match[1])
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Scheme+"://"+u.Host+"/stock-search-proxy/multi-search", bytes.NewBufferString(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("iMotor search status %d", resp.StatusCode)
	}
	var search imotorSearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(&search); err != nil || len(search.Results) == 0 || len(search.Results[0].Hits) == 0 {
		return "", fmt.Errorf("iMotor search returned no inventory: %w", err)
	}
	var cards strings.Builder
	cards.WriteString(`<section data-imotor-api-inventory="true">`)
	vehicleMaps := make([]map[string]any, 0, len(search.Results[0].Hits))
	for _, hit := range search.Results[0].Hits {
		if hit.ID <= 0 {
			continue
		}
		title := strings.TrimSpace(fmt.Sprintf("%d %s %s", hit.Year, hit.Make, hit.Model))
		vehicleType := strings.ToLower(strings.TrimSpace(hit.VehicleType))
		if vehicleType == "" {
			vehicleType = "used"
		}
		detailURL := fmt.Sprintf("/%s-trucks/for sale/%s/%s/%d/%d/", slugIMotor(vehicleType), slugIMotor(hit.Make), slugIMotor(hit.Model), hit.Year, hit.ID)
		vehicleMaps = append(vehicleMaps, map[string]any{
			"stockid": strconv.Itoa(hit.ID), "url": detailURL, "title": title,
			"year": hit.Year, "make": hit.Make, "model": hit.Model, "price": hit.Price,
			"mileage": hit.Odometer, "vin": validVINCandidate(hit.VIN), "images": hit.Images,
		})
		cards.WriteString(`<div class="classStockV5CardWrapper">`)
		cards.WriteString(`<a href="` + html.EscapeString(detailURL) + `"><h3>` + html.EscapeString(title) + `</h3></a>`)
		cards.WriteString(`<meta itemprop="vehicleIdentificationNumber" content="` + html.EscapeString(validVINCandidate(hit.VIN)) + `">`)
		cards.WriteString(`<button class="classSv5CardPriceButton"><span>$` + strconv.FormatFloat(hit.Price, 'f', 0, 64) + `</span></button>`)
		cards.WriteString(`<span data-testid="` + strconv.Itoa(hit.ID) + `-card-feature-odometer-value">` + strconv.FormatFloat(hit.Odometer, 'f', 0, 64) + ` Mi</span>`)
		if len(hit.Images) > 0 {
			cards.WriteString(`<div class="classEmbCarouselItem1"><img src="` + html.EscapeString(hit.Images[0]) + `"></div>`)
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

func getIMotorBytes(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 5<<20))
}

func slugIMotor(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(raw, "-")
	return strings.Trim(raw, "-")
}
