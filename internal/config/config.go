package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Port                 string
	ServiceKey           string
	HMACSecret           string
	EnableHMAC           bool
	RequestBodyLimitMB   int64
	RateLimitRPS         int
	RateLimitBurst       int
	MongoURI             string
	MongoDBName          string
	MongoCollection      string
	SiteConfigsDir       string
	DefaultRunTimeoutSec int
	Concurrency          int
	EnableCron           bool
	CronSpec             string
	CronDealershipID     string
	CronSourceURL        string
	Headless             bool
	AllowMainDBWrite     bool
	EnableCodexDiscovery bool
	OpenAIAPIKey         string
	OpenAIModel          string
}

func Load() (Config, error) {
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
	setDefaults()

	cfg := Config{
		Port:                 viper.GetString("PORT"),
		ServiceKey:           viper.GetString("SERVICE_KEY"),
		HMACSecret:           viper.GetString("HMAC_SECRET"),
		EnableHMAC:           viper.GetBool("ENABLE_HMAC"),
		RequestBodyLimitMB:   viper.GetInt64("REQUEST_BODY_LIMIT_MB"),
		RateLimitRPS:         viper.GetInt("RATE_LIMIT_RPS"),
		RateLimitBurst:       viper.GetInt("RATE_LIMIT_BURST"),
		MongoURI:             viper.GetString("MONGO_URI"),
		MongoDBName:          viper.GetString("MONGO_DB_NAME"),
		MongoCollection:      viper.GetString("MONGO_COLLECTION"),
		SiteConfigsDir:       viper.GetString("SITE_CONFIGS_DIR"),
		DefaultRunTimeoutSec: viper.GetInt("DEFAULT_RUN_TIMEOUT_SEC"),
		Concurrency:          viper.GetInt("SCRAPE_CONCURRENCY"),
		EnableCron:           viper.GetBool("ENABLE_CRON"),
		CronSpec:             viper.GetString("CRON_SPEC"),
		CronDealershipID:     viper.GetString("CRON_DEALERSHIP_ID"),
		CronSourceURL:        viper.GetString("CRON_SOURCE_URL"),
		Headless:             viper.GetBool("CHROME_HEADLESS"),
		AllowMainDBWrite:     viper.GetBool("ALLOW_MAIN_DB_WRITE"),
		EnableCodexDiscovery: viper.GetBool("ENABLE_CODEX_DISCOVERY"),
		OpenAIAPIKey:         viper.GetString("OPENAI_API_KEY"),
		OpenAIModel:          viper.GetString("OPENAI_MODEL"),
	}
	if cfg.ServiceKey == "" {
		return Config{}, fmt.Errorf("SERVICE_KEY is required")
	}
	if cfg.MongoURI == "" {
		return Config{}, fmt.Errorf("MONGO_URI is required")
	}
	if cfg.EnableCodexDiscovery && cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY is required when ENABLE_CODEX_DISCOVERY=true")
	}
	return cfg, nil
}

func (c Config) DefaultRunTimeout() time.Duration {
	return time.Duration(c.DefaultRunTimeoutSec) * time.Second
}

func setDefaults() {
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("REQUEST_BODY_LIMIT_MB", 2)
	viper.SetDefault("RATE_LIMIT_RPS", 5)
	viper.SetDefault("RATE_LIMIT_BURST", 10)
	viper.SetDefault("MONGO_DB_NAME", "inventory_scraper")
	viper.SetDefault("MONGO_COLLECTION", "run_logs")
	viper.SetDefault("SITE_CONFIGS_DIR", "configs/sites")
	viper.SetDefault("DEFAULT_RUN_TIMEOUT_SEC", 180)
	viper.SetDefault("SCRAPE_CONCURRENCY", 4)
	viper.SetDefault("ENABLE_CRON", false)
	viper.SetDefault("CRON_SPEC", "0 0 2 * * *")
	viper.SetDefault("CRON_DEALERSHIP_ID", "txtcharlie")
	viper.SetDefault("CRON_SOURCE_URL", "https://www.txtcharlie.com/inventory/")
	viper.SetDefault("CHROME_HEADLESS", true)
	viper.SetDefault("ALLOW_MAIN_DB_WRITE", false)
	viper.SetDefault("ENABLE_CODEX_DISCOVERY", false)
	viper.SetDefault("OPENAI_MODEL", "gpt-4.1-mini")
}
