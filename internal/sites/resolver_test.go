package sites

import (
	"context"
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func TestResolver_DoesNotUseDealershipFallbackWhenURLKeyMissing(t *testing.T) {
	loader := config.NewLoader("")
	loader.Cache("test_del_test_12333", config.SiteConfig{
		Name:    "test_del_test_12333",
		BaseURL: "https://www.idealcarsaz.com",
		ListPage: config.ListPageConfig{
			CardSelector: ".vehicle-card",
		},
	})
	r := Resolver{
		Loader: loader,
	}
	_, err := r.Resolve(context.Background(), "test_del_test_12333", "https://www.txtcharlie.com/inventory/")
	if err == nil {
		t.Fatalf("expected error when url-key config is missing and discovery is disabled")
	}
}
