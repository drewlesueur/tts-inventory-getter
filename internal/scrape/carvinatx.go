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

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type carvinListing struct {
	ID int `json:"id"`
	Title string `json:"title"`
	Link string `json:"link"`
	VIN string `json:"vin"`
	Year json.RawMessage `json:"year"`
	Make string `json:"make"`
	Model string `json:"model"`
	Price json.RawMessage `json:"price"`
	Mileage json.RawMessage `json:"mileage"`
	Color string `json:"color"`
	Engine string `json:"engine"`
	Drivetrain string `json:"drivetrain"`
}

type carvinMedia struct {
	ID int `json:"id"`
	SourceURL string `json:"source_url"`
	MediaType string `json:"media_type"`
}

type carvinPost struct {
	ID int `json:"id"`
	FeaturedMedia int `json:"featured_media"`
	Meta map[string]json.RawMessage `json:"meta"`
	Embedded struct {
		Terms [][]struct {
			Taxonomy string `json:"taxonomy"`
			Name string `json:"name"`
		} `json:"wp:term"`
	} `json:"_embedded"`
}

// The site's own inventory endpoint determines membership. The WordPress
// collection contains sold cars too, so it must only enrich those exact IDs.
// Stock comes from each post's metadata, never the unrelated DEBUG POST 203
// script repeated on every page, nor the VIN suffix displayed on listing cards.
func (s Service) scrapeCarvinATX(ctx context.Context, sourceURL string, site config.SiteConfig, opts Options) RunResult {
	result := RunResult{}
	fail := func(err error) RunResult {
		result.Errors = append(result.Errors, model.StructuredError{Code: "CARVIN_API_FAILED", Message: err.Error()})
		return result
	}
	u, _ := url.Parse(sourceURL)
	for key := range u.Query() {
		if !strings.HasPrefix(key, "utm_") && key != "gclid" && key != "fbclid" {
			return fail(fmt.Errorf("unsupported Carvin ATX inventory parameter %q", key))
		}
	}
	origin := u.Scheme + "://" + u.Host
	// Use the site's public JSON API directly. Browser-navigation headers on
	// the generic HTML fetcher can stall these endpoints, and browser/Python
	// dependencies are unnecessary for JSON.
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || (req.URL.Scheme != "https" && req.URL.Scheme != "http") || !strings.EqualFold(strings.TrimPrefix(req.URL.Hostname(), "www."), "carvinatx.com") || req.URL.User != nil || req.URL.Port() != "" {
			return fmt.Errorf("unexpected Carvin ATX API redirect")
		}
		return nil
	}}
	getJSON := func(path string, target any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin + path, nil)
		if err != nil { return err }
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil { return err }
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		if err != nil { return err }
		if resp.StatusCode != http.StatusOK {
			// WordPress returns 400 rather than [] past the last page. This
			// occurs when a gallery has an exact multiple of 100 images.
			var apiError struct { Code string `json:"code"` }
			pageURL, _ := url.Parse(path)
			page, _ := strconv.Atoi(pageURL.Query().Get("page"))
			if resp.StatusCode == http.StatusBadRequest && pageURL.Path == "/wp-json/wp/v2/media" && page > 1 && json.Unmarshal(body, &apiError) == nil && apiError.Code == "rest_post_invalid_page_number" {
				return nil
			}
			return fmt.Errorf("Carvin ATX API %s: HTTP %d", path, resp.StatusCode)
		}
		if err := json.Unmarshal(body, target); err != nil {
			return fmt.Errorf("Carvin ATX API %s: %w", path, err)
		}
		return nil
	}
	var listings []carvinListing
	if err := getJSON("/wp-json/carvinatx/v1/inventory", &listings); err != nil {
		return fail(err)
	}
	if len(listings) == 0 {
		return fail(fmt.Errorf("Carvin ATX inventory API returned no vehicles"))
	}
	if len(listings) > 2000 {
		return fail(fmt.Errorf("Carvin ATX inventory exceeds the 2000 vehicle limit"))
	}
	if limit := site.ListPage.MaxItems; limit > 0 && len(listings) > limit {
		listings = listings[:limit]
	}
	posts := make(map[int]carvinPost, len(listings))
	for start := 0; start < len(listings); start += 100 {
		end := start + 100
		if end > len(listings) { end = len(listings) }
		ids := make([]string, 0, end-start)
		for _, row := range listings[start:end] {
			if row.ID <= 0 { return fail(fmt.Errorf("Carvin ATX returned an invalid vehicle ID")) }
			ids = append(ids, strconv.Itoa(row.ID))
		}
		var batch []carvinPost
		if err := getJSON("/wp-json/wp/v2/vehicles?per_page=100&_embed=wp:term&include=" + strings.Join(ids, ","), &batch); err != nil {
			return fail(err)
		}
		for _, post := range batch { posts[post.ID] = post }
	}
	seenStocks, seenVINs := map[string]bool{}, map[string]bool{}
	for _, row := range listings {
		post, ok := posts[row.ID]
		// WordPress can omit a live listing from a collection response while
		// its individual published post remains available. Keep the ID match
		// and verify its VIN below instead of dropping that vehicle.
		if !ok {
			if err := getJSON(fmt.Sprintf("/wp-json/wp/v2/vehicles/%d?_embed=wp:term", row.ID), &post); err != nil { return fail(err) }
			ok = post.ID == row.ID
		}
		vin := validVINCandidate(row.VIN)
		if !ok || !carsCommerceVIN.MatchString(vin) || validVINCandidate(carsCommerceValue(post.Meta["_carvinatx_vin"])) != vin {
			return fail(fmt.Errorf("Carvin ATX vehicle %d is missing matching VIN metadata", row.ID))
		}
		stock := normalizeStockID(carsCommerceValue(post.Meta["_carvinatx_stock_number"]))
		if isMissingStockID(stock) || strings.EqualFold(stock, vin) {
			return fail(fmt.Errorf("Carvin ATX vehicle %d is missing stock metadata", row.ID))
		}
		if seenStocks[stock] || seenVINs[vin] {
			return fail(fmt.Errorf("Carvin ATX vehicle %d has a duplicate stock or VIN", row.ID))
		}
		seenStocks[stock], seenVINs[vin] = true, true
		link, err := url.Parse(row.Link)
		if err != nil || (link.Scheme != "https" && link.Scheme != "http") || link.User != nil || link.Port() != "" || !strings.EqualFold(strings.TrimPrefix(link.Hostname(), "www."), strings.TrimPrefix(u.Hostname(), "www.")) || !strings.HasPrefix(link.Path, "/vehicle/") {
			return fail(fmt.Errorf("Carvin ATX vehicle %d has an invalid detail URL", row.ID))
		}
		item := model.InventoryItem{
			StockID: stock, VIN: vin, URL: row.Link, Title: html.UnescapeString(row.Title),
			Year: carsCommerceValue(row.Year), Make: row.Make, Model: row.Model,
			Price: carsCommerceValue(row.Price), Mileage: carsCommerceValue(row.Mileage),
			Color: row.Color, Engine: row.Engine, DriveType: row.Drivetrain,
		}
		for _, terms := range post.Embedded.Terms {
			for _, term := range terms {
				switch term.Taxonomy {
				case "vehicle_body": item.BodyType = html.UnescapeString(term.Name)
				case "vehicle_fuel": item.FuelType = html.UnescapeString(term.Name)
				case "vehicle_transmission": item.Transmission = html.UnescapeString(term.Name)
				}
			}
		}
		// Attachment pagination preserves the full gallery, including more than
		// WordPress's default ten images. Never import the Carfax logo as a photo.
		for page := 1; ; page++ {
			if page > 20 { return fail(fmt.Errorf("Carvin ATX vehicle %d exceeds the gallery page limit", row.ID)) }
			var media []carvinMedia
			path := fmt.Sprintf("/wp-json/wp/v2/media?parent=%d&per_page=100&page=%d", row.ID, page)
			if err := getJSON(path, &media); err != nil { return fail(err) }
			for _, photo := range media {
				if photo.MediaType != "image" || photo.SourceURL == "" { continue }
				item.Images = append(item.Images, photo.SourceURL)
				if photo.ID == post.FeaturedMedia { item.PrimaryImage = photo.SourceURL }
			}
			if len(media) < 100 { break }
		}
		if item.PrimaryImage == "" && post.FeaturedMedia > 0 {
			var featured carvinMedia
			if err := getJSON(fmt.Sprintf("/wp-json/wp/v2/media/%d", post.FeaturedMedia), &featured); err != nil { return fail(err) }
			if featured.MediaType == "image" && featured.SourceURL != "" {
				item.PrimaryImage = featured.SourceURL
				item.Images = append([]string{featured.SourceURL}, item.Images...)
			}
		}
		if item.PrimaryImage == "" && len(item.Images) > 0 { item.PrimaryImage = item.Images[0] }
		item = applyItemAliases(NormalizeItem(sourceURL, item), opts.DealershipID, sourceURL)
		result.Items = append(result.Items, item)
		reportItemsProgress(opts, "details_completed", result.Items)
	}
	reportItemsProgress(opts, "completed", result.Items)
	return result
}
