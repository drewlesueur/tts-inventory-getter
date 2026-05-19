package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type SiteConfig struct {
	Name       string            `yaml:"name" json:"name"`
	BaseURL    string            `yaml:"baseUrl" json:"baseUrl"`
	ListPage   ListPageConfig    `yaml:"listPage" json:"listPage"`
	DetailPage DetailPageConfig  `yaml:"detailPage" json:"detailPage"`
	Regex      RegexConfig       `yaml:"regex" json:"regex"`
	Discovery  DiscoveryMetadata `yaml:"discovery,omitempty" json:"discovery,omitempty"`
}

type ListPageConfig struct {
	WaitSelectors   []string         `yaml:"waitSelectors" json:"waitSelectors"`
	CardSelector    string           `yaml:"cardSelector" json:"cardSelector"`
	TitleSelector   string           `yaml:"titleSelector" json:"titleSelector"`
	URLSelector     string           `yaml:"urlSelector" json:"urlSelector"`
	StockSelector   string           `yaml:"stockSelector" json:"stockSelector"`
	PriceSelector   string           `yaml:"priceSelector" json:"priceSelector"`
	MileageSelector string           `yaml:"mileageSelector" json:"mileageSelector"`
	ImageSelector   string           `yaml:"imageSelector" json:"imageSelector"`
	TotalSelector   string           `yaml:"totalSelector" json:"totalSelector"`
	MaxItems        int              `yaml:"maxItems,omitempty" json:"maxItems,omitempty"`
	Pagination      PaginationConfig `yaml:"pagination" json:"pagination"`
}

type PaginationConfig struct {
	Type              string `yaml:"type" json:"type"`
	NextSelector      string `yaml:"nextSelector" json:"nextSelector"`
	LoadMoreSelector  string `yaml:"loadMoreSelector" json:"loadMoreSelector"`
	MaxPages          int    `yaml:"maxPages" json:"maxPages"`
	InfiniteScroll    bool   `yaml:"infiniteScroll" json:"infiniteScroll"`
	ScrollMaxAttempts int    `yaml:"scrollMaxAttempts" json:"scrollMaxAttempts"`
	ClickMaxAttempts  int    `yaml:"clickMaxAttempts" json:"clickMaxAttempts"`
	ModeHint          string `yaml:"modeHint,omitempty" json:"modeHint,omitempty"`
}

type DetailPageConfig struct {
	ImageSelectors []string `yaml:"imageSelectors" json:"imageSelectors"`
	VINSelector    string   `yaml:"vinSelector" json:"vinSelector"`
	StockSelector  string   `yaml:"stockSelector" json:"stockSelector"`
}

type RegexConfig struct {
	Stock []string `yaml:"stock" json:"stock"`
	VIN   []string `yaml:"vin" json:"vin"`
}

type DiscoveryMetadata struct {
	Confidence   float64  `yaml:"confidence,omitempty" json:"confidence,omitempty"`
	Notes        string   `yaml:"notes,omitempty" json:"notes,omitempty"`
	TotalHints   []string `yaml:"totalHints,omitempty" json:"totalHints,omitempty"`
	PagingHints  []string `yaml:"pagingHints,omitempty" json:"pagingHints,omitempty"`
	DiscoveredAt string   `yaml:"discoveredAt,omitempty" json:"discoveredAt,omitempty"`
}

type Loader struct {
	Dir   string
	cache *siteCache
}

type siteCache struct {
	mu sync.RWMutex
	m  map[string]SiteConfig
}

func NewLoader(dir string) Loader {
	return Loader{Dir: dir, cache: &siteCache{m: map[string]SiteConfig{}}}
}

func (l Loader) LoadByName(name string) (SiteConfig, error) {
	if l.cache != nil {
		l.cache.mu.RLock()
		cfg, ok := l.cache.m[name]
		l.cache.mu.RUnlock()
		if ok {
			return cfg, nil
		}
	}
	return SiteConfig{}, fmt.Errorf("site config not cached: %s", name)
}

func (l Loader) WarmCache() (int, error) {
	if l.cache == nil || l.Dir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	loaded := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		cfg, err := l.LoadByPath(filepath.Join(l.Dir, name))
		if err != nil {
			continue
		}
		key := decodeCacheFilename(strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml"))
		l.Cache(key, cfg)
		loaded++
	}
	return loaded, nil
}

func (l Loader) LoadByPath(path string) (SiteConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SiteConfig{}, err
	}
	var cfg SiteConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return SiteConfig{}, err
	}
	if cfg.ListPage.CardSelector == "" {
		return SiteConfig{}, fmt.Errorf("invalid site config: missing cardSelector")
	}
	if cfg.ListPage.Pagination.ScrollMaxAttempts == 0 {
		cfg.ListPage.Pagination.ScrollMaxAttempts = 8
	}
	if cfg.ListPage.Pagination.ClickMaxAttempts == 0 {
		cfg.ListPage.Pagination.ClickMaxAttempts = 20
	}
	return cfg, nil
}

func (l Loader) Cache(name string, cfg SiteConfig) {
	if l.cache == nil {
		return
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	l.cache.mu.Lock()
	l.cache.m[name] = cfg
	l.cache.mu.Unlock()
}

func (l Loader) SaveByName(name string, cfg SiteConfig) error {
	if l.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(l.Dir, encodeCacheFilename(name)+".yaml")
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		return err
	}
	l.Cache(name, cfg)
	return nil
}

func (l Loader) DeleteByName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if l.cache != nil {
		l.cache.mu.Lock()
		delete(l.cache.m, name)
		l.cache.mu.Unlock()
	}
	if l.Dir == "" {
		return nil
	}
	p := filepath.Join(l.Dir, encodeCacheFilename(name)+".yaml")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (l Loader) ClearCacheFiles() error {
	if l.cache != nil {
		l.cache.mu.Lock()
		l.cache.m = map[string]SiteConfig{}
		l.cache.mu.Unlock()
	}
	if l.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		if err := os.Remove(filepath.Join(l.Dir, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (l Loader) IsCacheFresh(name string, maxAge time.Duration) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if maxAge <= 0 {
		return true
	}
	if l.Dir == "" {
		return true
	}
	p := filepath.Join(l.Dir, encodeCacheFilename(name)+".yaml")
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) <= maxAge
}

func encodeCacheFilename(key string) string {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "url::") {
		enc := base64.RawURLEncoding.EncodeToString([]byte(key))
		return "urlkey_" + enc
	}
	return key
}

func decodeCacheFilename(name string) string {
	if strings.HasPrefix(name, "urlkey_") {
		raw := strings.TrimPrefix(name, "urlkey_")
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err == nil {
			return string(b)
		}
	}
	return name
}
