package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/discovery"
)

type fakeFetcher struct {
	html string
	err  error
}

func (f fakeFetcher) Fetch(_ context.Context, _ string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.html, nil
}

type fakeDiscover struct {
	err error
}

func (d fakeDiscover) Discover(_ context.Context, _ string, _ string) (config.SiteConfig, error) {
	if d.err != nil {
		return config.SiteConfig{}, d.err
	}
	return config.SiteConfig{
		Name:    "discovered",
		BaseURL: "https://example.com",
		ListPage: config.ListPageConfig{
			CardSelector: ".card",
		},
	}, nil
}

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

func TestResolver_UsesStaleURLKeyConfigWhenDiscoveryDisabled(t *testing.T) {
	loader := config.NewLoader("")
	loader.Cache("url::www.idealcarsaz.com/used-cars-in-mesa-az", config.SiteConfig{
		Name:    "url::www.idealcarsaz.com/used-cars-in-mesa-az",
		BaseURL: "https://www.idealcarsaz.com/used-cars-in-mesa-az",
		ListPage: config.ListPageConfig{
			CardSelector: ".vehicle-card",
		},
	})
	r := Resolver{
		Loader: loader,
	}

	site, err := r.Resolve(context.Background(), "dealer", "https://www.idealcarsaz.com/used-cars-in-mesa-az")
	if err != nil {
		t.Fatalf("expected stale site config fallback, got error: %v", err)
	}
	if site.ListPage.CardSelector != ".vehicle-card" {
		t.Fatalf("expected cached card selector, got %q", site.ListPage.CardSelector)
	}
}

func TestResolver_UsesStaleURLKeyConfigWhenDiscoveryFails(t *testing.T) {
	dir := t.TempDir()
	loader := config.NewLoader(dir)
	key := "url::www.idealcarsaz.com/used-cars-in-mesa-az"
	saved := config.SiteConfig{
		Name:    "url::www.idealcarsaz.com/used-cars-in-mesa-az",
		BaseURL: "https://www.idealcarsaz.com/used-cars-in-mesa-az",
		ListPage: config.ListPageConfig{
			CardSelector: ".vehicle-card",
		},
	}
	if err := loader.SaveByName(key, saved); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one saved config: entries=%d err=%v", len(entries), err)
	}
	old := time.Now().AddDate(-1, 0, 0)
	if err := os.Chtimes(filepath.Join(dir, entries[0].Name()), old, old); err != nil {
		t.Fatal(err)
	}
	discoverClient := &discovery.Client{}
	r := Resolver{
		Loader:   loader,
		Discover: discoverClient,
		Fetcher:  fakeFetcher{html: "<html></html>"},
	}

	// Even a year-old template is permanent and must bypass discovery.
	discoverClient.APIKey = ""
	discoverClient.Model = "gpt-4.1-mini"

	site, err := r.Resolve(context.Background(), "dealer", "https://www.idealcarsaz.com/used-cars-in-mesa-az")
	if err != nil {
		t.Fatalf("expected stale site config fallback on discovery failure, got error: %v", err)
	}
	if site.ListPage.CardSelector != ".vehicle-card" {
		t.Fatalf("expected cached card selector, got %q", site.ListPage.CardSelector)
	}
}

func TestResolver_ReturnsDiscoveryErrorWithoutCache(t *testing.T) {
	loader := config.NewLoader("")
	r := Resolver{
		Loader:  loader,
		Fetcher: fakeFetcher{err: errors.New("fetch failed")},
	}
	_, err := r.Resolve(context.Background(), "dealer", "https://www.idealcarsaz.com/used-cars-in-mesa-az")
	if err == nil {
		t.Fatalf("expected error when cache missing and discovery unavailable")
	}
}
