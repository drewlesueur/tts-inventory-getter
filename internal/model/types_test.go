package model

import (
	"encoding/json"
	"testing"
)

func TestInventoryItemMarshalJSONIncludesFullShape(t *testing.T) {
	b, err := json.Marshal(InventoryItem{StockID: "S1", URL: "https://dealer.test/car", Title: "2024 Test Car"})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	required := []string{
		"stockId", "stock", "url", "website", "dealerId", "title", "style", "year", "make", "model",
		"color", "vin", "primaryImage", "images", "photoURLs", "price", "vehicleListPrice", "mileage",
		"engine", "cylinders", "horsepower", "torque", "transmission", "transmissionType", "driveType",
		"fuelType", "fuelCapacity", "fuelEconomy", "milesPerGallon", "milesPerLiter", "cityMPG",
		"highwayMPG", "cityMPL", "highwayMPL", "bodyType", "seatInfo", "passengerCapacity",
		"tireInfo", "frontTire", "rearTire", "wheelInfo", "frontWheel", "rearWheel",
	}
	for _, key := range required {
		if _, ok := got[key]; !ok {
			t.Fatalf("expected key %q in JSON: %s", key, string(b))
		}
	}
	if imgs, ok := got["images"].([]any); !ok || len(imgs) != 0 {
		t.Fatalf("expected images to be empty array, got %#v", got["images"])
	}
	if photos, ok := got["photoURLs"].([]any); !ok || len(photos) != 0 {
		t.Fatalf("expected photoURLs to be empty array, got %#v", got["photoURLs"])
	}
}
