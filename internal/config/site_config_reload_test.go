package config

import "testing"

func TestLoaderReloadsUpdatedPermanentTemplate(t *testing.T) {
	loader := NewLoader(t.TempDir())
	key := "url::dealer.test/inventory"
	first := SiteConfig{ListPage: ListPageConfig{CardSelector: ".old-card"}}
	if err := loader.SaveByName(key, first); err != nil {
		t.Fatal(err)
	}
	if got, err := loader.LoadByName(key); err != nil || got.ListPage.CardSelector != ".old-card" {
		t.Fatalf("initial load = %+v, %v", got, err)
	}
	updated := SiteConfig{ListPage: ListPageConfig{CardSelector: "[data-vehicle-card]"}}
	if err := loader.SaveByName(key, updated); err != nil {
		t.Fatal(err)
	}
	// Seed stale memory to prove the persisted YAML remains authoritative.
	loader.Cache(key, first)
	got, err := loader.LoadByName(key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ListPage.CardSelector != "[data-vehicle-card]" {
		t.Fatalf("updated template was hidden by memory cache: %+v", got)
	}
}
