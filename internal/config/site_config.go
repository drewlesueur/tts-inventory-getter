package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type SiteConfig struct {
	Name       string         `yaml:"name" json:"name"`
	BaseURL    string         `yaml:"baseUrl" json:"baseUrl"`
	ListPage   ListPageConfig `yaml:"listPage" json:"listPage"`
	DetailPage DetailPageConfig `yaml:"detailPage" json:"detailPage"`
	Regex      RegexConfig    `yaml:"regex" json:"regex"`
}

type ListPageConfig struct {
	WaitSelectors  []string `yaml:"waitSelectors" json:"waitSelectors"`
	CardSelector   string   `yaml:"cardSelector" json:"cardSelector"`
	TitleSelector  string   `yaml:"titleSelector" json:"titleSelector"`
	URLSelector    string   `yaml:"urlSelector" json:"urlSelector"`
	StockSelector  string   `yaml:"stockSelector" json:"stockSelector"`
	PriceSelector  string   `yaml:"priceSelector" json:"priceSelector"`
	MileageSelector string  `yaml:"mileageSelector" json:"mileageSelector"`
	ImageSelector  string   `yaml:"imageSelector" json:"imageSelector"`
	Pagination     PaginationConfig `yaml:"pagination" json:"pagination"`
}

type PaginationConfig struct {
	Type              string `yaml:"type" json:"type"`
	NextSelector      string `yaml:"nextSelector" json:"nextSelector"`
	MaxPages          int    `yaml:"maxPages" json:"maxPages"`
	InfiniteScroll    bool   `yaml:"infiniteScroll" json:"infiniteScroll"`
	ScrollMaxAttempts int    `yaml:"scrollMaxAttempts" json:"scrollMaxAttempts"`
}

type DetailPageConfig struct {
	ImageSelectors []string `yaml:"imageSelectors" json:"imageSelectors"`
	VINSelector    string   `yaml:"vinSelector" json:"vinSelector"`
}

type RegexConfig struct {
	Stock []string `yaml:"stock" json:"stock"`
	VIN   []string `yaml:"vin" json:"vin"`
}

type Loader struct { Dir string }

func (l Loader) LoadByName(name string) (SiteConfig, error) {
	p := filepath.Join(l.Dir, name+".yaml")
	return l.LoadByPath(p)
}

func (l Loader) LoadByPath(path string) (SiteConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil { return SiteConfig{}, err }
	var cfg SiteConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil { return SiteConfig{}, err }
	if cfg.ListPage.CardSelector == "" {
		return SiteConfig{}, fmt.Errorf("invalid site config: missing cardSelector")
	}
	if cfg.ListPage.Pagination.MaxPages == 0 { cfg.ListPage.Pagination.MaxPages = 5 }
	if cfg.ListPage.Pagination.ScrollMaxAttempts == 0 { cfg.ListPage.Pagination.ScrollMaxAttempts = 8 }
	return cfg, nil
}
