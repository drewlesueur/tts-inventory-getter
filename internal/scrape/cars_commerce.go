package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

var carsCommerceVIN = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)

type carsCommerceListing struct {
	VIN     string          `json:"vin"`
	Stock   string          `json:"stock"`
	Type    string          `json:"type"`
	Year    json.RawMessage `json:"year"`
	Make    string          `json:"make"`
	Model   string          `json:"model"`
	Trim    string          `json:"trim"`
	Mileage json.RawMessage `json:"mileage"`
	URL     string          `json:"vdp_url"`
	Pricing struct {
		OurPrice json.RawMessage `json:"our_price"`
	} `json:"pricing"`
	Styles struct {
		Color string `json:"exterior_color"`
	} `json:"styles"`
	Mechanical struct {
		Engine       string          `json:"engine"`
		Transmission string          `json:"transmission"`
		TransmissionType string      `json:"transmission_type"`
		DriveType    string          `json:"drivetrain"`
		FuelType     string          `json:"fuel_type"`
		Cylinders    json.RawMessage `json:"engine_cylinders"`
		Horsepower   json.RawMessage `json:"horsepower"`
		Torque       json.RawMessage `json:"torque"`
		FuelCapacity json.RawMessage `json:"fuel_tank_capacity"`
		CityMPG      json.RawMessage `json:"city_mpg"`
		HighwayMPG   json.RawMessage `json:"highway_mpg"`
	} `json:"mechanical"`
	Body struct {
		Type string `json:"type"`
	} `json:"body_details"`
	Capacity json.RawMessage `json:"passenger_capacity"`
	Media struct {
		Images []string `json:"images"`
	} `json:"media"`
	Extra map[string]json.RawMessage `json:"extra_fields"`
}

type carsCommerceResponse struct {
	Data struct {
		Listings []carsCommerceListing `json:"listings"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			Total       int `json:"total"`
			Count       int `json:"count"`
			CurrentPage int `json:"current_page"`
			TotalPages  int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

// The template supplies the page's default type. Never silently ignore a URL
// refinement: doing so could import a different inventory than the user chose.
func carsCommerceSort(sourceURL string, site config.SiteConfig) (string, error) {
	u, err := url.Parse(sourceURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(site.BaseURL)
	if err != nil || !strings.EqualFold(u.Host, base.Host) || strings.TrimSuffix(u.Path, "/") != strings.TrimSuffix(base.Path, "/") {
		return "", fmt.Errorf("Cars Commerce template does not match source host/path")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", err
	}
	sortField := "low_price"
	for key, values := range query {
		if strings.HasPrefix(key, "utm_") || key == "gclid" || key == "fbclid" {
			continue
		}
		if len(values) != 1 {
			return "", fmt.Errorf("unsupported repeated inventory parameter %q", key)
		}
		switch key {
		case "_dFR[type][0]":
			if values[0] != site.CarsCommerce.VehicleType {
				return "", fmt.Errorf("inventory type does not match configured %q page", site.CarsCommerce.VehicleType)
			}
		case "idx":
			if values[0] != "year_high_to_low" {
				return "", fmt.Errorf("unsupported inventory sort %q", values[0])
			}
			sortField = "year"
		default:
			return "", fmt.Errorf("unsupported inventory query parameter %q", key)
		}
	}
	return sortField, nil
}

// Keep published zero/blank values; missing or null optional fields stay blank.
func carsCommerceValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func carsCommerceItem(row carsCommerceListing) model.InventoryItem {
	price := carsCommerceValue(row.Pricing.OurPrice)
	// Lightning's displayed sale price includes dealer fees. It can also be
	// false, so only override the base price when a value is actually supplied.
	var lightning struct {
		Pricing struct {
			Low struct { Value json.RawMessage `json:"value"` } `json:"low"`
		} `json:"pricing"`
	}
	if json.Unmarshal(row.Extra["lightning"], &lightning) == nil && len(lightning.Pricing.Low.Value) > 0 && string(lightning.Pricing.Low.Value) != "null" {
		price = carsCommerceValue(lightning.Pricing.Low.Value)
	}
	item := model.InventoryItem{
		VIN: row.VIN, StockID: row.Stock, Year: carsCommerceValue(row.Year),
		Make: row.Make, Model: row.Model, Style: row.Trim, URL: row.URL,
		Title: carsCommerceValue(row.Extra["title"]), Price: price,
		Mileage: carsCommerceValue(row.Mileage), Color: row.Styles.Color,
		Images: row.Media.Images, Engine: row.Mechanical.Engine,
		Transmission: row.Mechanical.Transmission, DriveType: row.Mechanical.DriveType,
		TransmissionType: row.Mechanical.TransmissionType,
		FuelType: row.Mechanical.FuelType, Cylinders: carsCommerceValue(row.Mechanical.Cylinders),
		Horsepower: carsCommerceValue(row.Mechanical.Horsepower), Torque: carsCommerceValue(row.Mechanical.Torque),
		FuelCapacity: carsCommerceValue(row.Mechanical.FuelCapacity), PassengerCapacity: carsCommerceValue(row.Capacity),
		CityMPG: carsCommerceValue(row.Mechanical.CityMPG), HighwayMPG: carsCommerceValue(row.Mechanical.HighwayMPG),
		BodyType: row.Body.Type,
	}
	if item.Title == "" {
		item.Title = strings.TrimSpace(strings.Join([]string{row.Type, item.Year, row.Make, row.Model, row.Trim}, " "))
	}
	if len(item.Images) > 0 {
		item.PrimaryImage = item.Images[0]
	}
	return item
}

// Fail without items on an incomplete response, so partial inventory cannot
// replace the dealership's full inventory. Existing async retries still apply.
func scrapeCarsCommerce(ctx context.Context, sourceURL string, site config.SiteConfig, opts Options) RunResult {
	fail := func(err error) RunResult {
		return RunResult{Errors: []model.StructuredError{{Code: "CARS_COMMERCE_FETCH_FAILED", Message: err.Error()}}}
	}
	cfg := site.CarsCommerce
	if cfg.CCID == "" || strings.IndexFunc(cfg.CCID, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || strings.TrimSpace(cfg.SearchKey) == "" || cfg.VehicleType == "" {
		return fail(fmt.Errorf("Cars Commerce ccid, public search key and vehicle type are required"))
	}
	sortField, err := carsCommerceSort(sourceURL, site)
	if err != nil {
		return fail(err)
	}
	order := "asc"
	if sortField == "year" {
		order = "desc"
	}
	maxPages := site.ListPage.Pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 20
	}
	client := &http.Client{Timeout: 30 * time.Second}
	endpoint := "https://websites-search.api.carscommerce.inc/api/v1/listings/" + cfg.CCID + "/search"
	items := make([]model.InventoryItem, 0)
	seenVIN, seenStock := map[string]bool{}, map[string]bool{}
	total, pages := -1, 1
	for page := 1; page <= pages; page++ {
		body, err := json.Marshal(map[string]interface{}{
			"page": page, "perPage": 100,
			"sort": []map[string]string{{"field": sortField, "order": order}},
			"filters": map[string]interface{}{"status": []string{"publish", "modified", "pend-sale"}},
			"facetFilters": map[string][]string{"type_slug": {cfg.VehicleType}},
			"requestedFields": []string{"vin", "stock", "type", "year", "make", "model", "trim", "mileage", "vdp_url", "pricing", "styles", "mechanical", "body_details", "passenger_capacity", "media", "extra_fields"},
		})
		if err != nil {
			return fail(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fail(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-api-key", cfg.SearchKey)
		resp, err := client.Do(req)
		if err != nil {
			return fail(fmt.Errorf("Cars Commerce page %d: %w", page, err))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
		resp.Body.Close()
		if readErr != nil {
			return fail(readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fail(fmt.Errorf("Cars Commerce page %d returned HTTP %d", page, resp.StatusCode))
		}
		if len(data) > 16*1024*1024 {
			return fail(fmt.Errorf("Cars Commerce page %d exceeded response limit", page))
		}
		var result carsCommerceResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return fail(fmt.Errorf("Cars Commerce page %d: %w", page, err))
		}
		p := result.Meta.Pagination
		if p.Total < 0 || p.CurrentPage != page || p.TotalPages < 1 || p.Count != len(result.Data.Listings) {
			return fail(fmt.Errorf("invalid Cars Commerce pagination on page %d", page))
		}
		if total == -1 {
			total, pages = p.Total, p.TotalPages
		}
		if p.Total != total || p.TotalPages != pages || pages > maxPages || (p.Count == 0 && total > 0) {
			return fail(fmt.Errorf("incomplete or changing Cars Commerce inventory on page %d", page))
		}
		for _, row := range result.Data.Listings {
			if row.Type != cfg.VehicleType {
				return fail(fmt.Errorf("Cars Commerce returned a vehicle outside the %q filter", cfg.VehicleType))
			}
			item := NormalizeItem(sourceURL, carsCommerceItem(row))
			vin := strings.ToUpper(strings.TrimSpace(row.VIN))
			stock := normalizeStockID(row.Stock)
			if !carsCommerceVIN.MatchString(vin) || validVINCandidate(vin) == "" || item.VIN != vin || isMissingStockID(stock) || item.StockID != stock || item.URL == "" || seenVIN[item.VIN] || seenStock[item.StockID] {
				return fail(fmt.Errorf("missing or duplicate vehicle identity on Cars Commerce page %d", page))
			}
			seenVIN[item.VIN], seenStock[item.StockID] = true, true
			items = append(items, applyItemAliases(item, opts.DealershipID, sourceURL))
		}
		reportItemsProgress(opts, "pages_collected", items)
	}
	if len(items) != total || total == 0 {
		return fail(fmt.Errorf("Cars Commerce returned %d of %d vehicles", len(items), total))
	}
	if site.ListPage.MaxItems > 0 && len(items) > site.ListPage.MaxItems {
		return fail(fmt.Errorf("Cars Commerce inventory exceeds configured item limit"))
	}
	reportItemsProgress(opts, "completed", items)
	return RunResult{Items: items}
}
