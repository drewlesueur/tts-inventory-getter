package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

type AIEnricher struct {
	APIKey  string
	Model   string
	BaseURL string
	Client  *http.Client
}

type aiResp struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (a *AIEnricher) Enrich(ctx context.Context, item model.InventoryItem, website string) (model.InventoryItem, error) {
	if a == nil || strings.TrimSpace(a.APIKey) == "" {
		return item, nil
	}
	modelName := strings.TrimSpace(a.Model)
	if modelName == "" {
		modelName = "gpt-5"
	}
	baseURL := strings.TrimSpace(a.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1/responses"
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}

	prompt := map[string]any{
		"website":     website,
		"item":        item,
		"instruction": "Fill only missing vehicle fields when confidence is high. Keep existing values unchanged.",
	}
	pb, _ := json.Marshal(prompt)

	payload := map[string]any{
		"model": modelName,
		"input": []map[string]any{
			{"role": "system", "content": []map[string]string{{"type": "input_text", "text": "Return JSON only. Fill unknown vehicle fields conservatively."}}},
			{"role": "user", "content": []map[string]string{{"type": "input_text", "text": string(pb)}}},
		},
		"text": map[string]any{
			"format": map[string]any{
				"type": "json_schema",
				"name": "vehicle_enrichment",
				"schema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"vin":              map[string]any{"type": "string"},
						"engine":           map[string]any{"type": "string"},
						"transmission":     map[string]any{"type": "string"},
						"driveType":        map[string]any{"type": "string"},
						"color":            map[string]any{"type": "string"},
						"make":             map[string]any{"type": "string"},
						"model":            map[string]any{"type": "string"},
						"year":             map[string]any{"type": "string"},
						"stockId":          map[string]any{"type": "string"},
						"price":            map[string]any{"type": "string"},
						"mileage":          map[string]any{"type": "string"},
						"primaryImage":     map[string]any{"type": "string"},
						"images":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"style":            map[string]any{"type": "string"},
						"vehicleListPrice": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return item, err
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return item, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return item, fmt.Errorf("openai enrich failed status=%d body=%s", resp.StatusCode, string(raw))
	}

	var out aiResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return item, err
	}
	text := ""
	for _, o := range out.Output {
		for _, c := range o.Content {
			if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
				text = c.Text
				break
			}
		}
		if text != "" {
			break
		}
	}
	if text == "" {
		return item, nil
	}
	var p model.InventoryItem
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return item, nil
	}
	return mergeMissingFields(item, p), nil
}

func mergeMissingFields(dst, src model.InventoryItem) model.InventoryItem {
	if dst.VIN == "" {
		dst.VIN = src.VIN
	}
	if dst.Engine == "" {
		dst.Engine = src.Engine
	}
	if dst.Cylinders == "" {
		dst.Cylinders = src.Cylinders
	}
	if dst.Horsepower == "" {
		dst.Horsepower = src.Horsepower
	}
	if dst.Torque == "" {
		dst.Torque = src.Torque
	}
	if dst.Transmission == "" {
		dst.Transmission = src.Transmission
	}
	if dst.TransmissionType == "" {
		dst.TransmissionType = src.TransmissionType
	}
	if dst.DriveType == "" {
		dst.DriveType = src.DriveType
	}
	if dst.FuelType == "" {
		dst.FuelType = src.FuelType
	}
	if dst.FuelCapacity == "" {
		dst.FuelCapacity = src.FuelCapacity
	}
	if dst.FuelEconomy == "" {
		dst.FuelEconomy = src.FuelEconomy
	}
	if dst.MilesPerGallon == "" {
		dst.MilesPerGallon = src.MilesPerGallon
	}
	if dst.MilesPerLiter == "" {
		dst.MilesPerLiter = src.MilesPerLiter
	}
	if dst.CityMPG == "" {
		dst.CityMPG = src.CityMPG
	}
	if dst.HighwayMPG == "" {
		dst.HighwayMPG = src.HighwayMPG
	}
	if dst.CityMPL == "" {
		dst.CityMPL = src.CityMPL
	}
	if dst.HighwayMPL == "" {
		dst.HighwayMPL = src.HighwayMPL
	}
	if dst.BodyType == "" {
		dst.BodyType = src.BodyType
	}
	if dst.SeatInfo == "" {
		dst.SeatInfo = src.SeatInfo
	}
	if dst.PassengerCapacity == "" {
		dst.PassengerCapacity = src.PassengerCapacity
	}
	if dst.TireInfo == "" {
		dst.TireInfo = src.TireInfo
	}
	if dst.FrontTire == "" {
		dst.FrontTire = src.FrontTire
	}
	if dst.RearTire == "" {
		dst.RearTire = src.RearTire
	}
	if dst.WheelInfo == "" {
		dst.WheelInfo = src.WheelInfo
	}
	if dst.FrontWheel == "" {
		dst.FrontWheel = src.FrontWheel
	}
	if dst.RearWheel == "" {
		dst.RearWheel = src.RearWheel
	}
	if dst.Color == "" {
		dst.Color = src.Color
	}
	if dst.Make == "" {
		dst.Make = src.Make
	}
	if dst.Model == "" {
		dst.Model = src.Model
	}
	if dst.Year == "" {
		dst.Year = src.Year
	}
	if dst.StockID == "" {
		dst.StockID = src.StockID
	}
	if dst.Price == "" {
		dst.Price = src.Price
	}
	if dst.VehicleListPrice == "" {
		dst.VehicleListPrice = src.VehicleListPrice
	}
	if dst.Mileage == "" {
		dst.Mileage = src.Mileage
	}
	if dst.PrimaryImage == "" {
		dst.PrimaryImage = src.PrimaryImage
	}
	if len(dst.Images) == 0 {
		dst.Images = src.Images
	}
	if dst.Style == "" {
		dst.Style = src.Style
	}
	return dst
}
