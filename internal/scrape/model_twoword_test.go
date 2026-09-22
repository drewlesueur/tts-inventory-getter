package scrape

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

// Titles whose model name is two words: taking a single token after the make
// collapsed every Tesla into model "Model" (mountainautoslc.com, 34 of 35 cars).
func TestNormalizeItemTwoWordModels(t *testing.T) {
	cases := []struct {
		title     string
		wantMake  string
		wantModel string
	}{
		{"2022 TESLA MODEL Y FSD RYZEN ALL WHEEL DRIVE", "TESLA", "MODEL Y"},
		{"2022 TESLA MODEL 3 LONG RANGE AWD", "TESLA", "MODEL 3"},
		{"Used 2021 Jeep Grand Cherokee Laredo", "Jeep", "Grand Cherokee"},
		{"2019 Hyundai Santa Fe SEL", "Hyundai", "Santa Fe"},
		// Single-word models must be untouched.
		{"2022 RIVIAN R1T ADVENTURE", "RIVIAN", "R1T"},
		{"Used 2013 Mazda Mazda3 i SV", "Mazda", "Mazda3"},
		{"2017 Audi A6 Premium Plus", "Audi", "A6"},
	}
	for _, c := range cases {
		got := NormalizeItem("https://example.com/inventory", model.InventoryItem{Title: c.title})
		if got.Make != c.wantMake || got.Model != c.wantModel {
			t.Errorf("%q => make=%q model=%q, want make=%q model=%q",
				c.title, got.Make, got.Model, c.wantMake, c.wantModel)
		}
	}
}

// A trailing prefix word with nothing after it must not panic or append empty.
func TestNormalizeItemTwoWordModelTruncated(t *testing.T) {
	got := NormalizeItem("https://example.com/inventory", model.InventoryItem{Title: "2022 TESLA MODEL"})
	if got.Model != "" && got.Model != "MODEL" {
		t.Errorf("unexpected model %q for a truncated title", got.Model)
	}
}
