package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Port                       string
	ServiceKey                 string
	HMACSecret                 string
	EnableHMAC                 bool
	RequestBodyLimitMB         int64
	RateLimitRPS               int
	RateLimitBurst             int
	SQLitePath                 string
	SiteConfigsDir             string
	DefaultRunTimeoutSec       int
	Concurrency                int
	ScrapeMaxAttempts          int
	ScrapeRetryBackoffSec      int
	HTTPFetchTimeoutSec        int
	EnableDailyUpsertCron      bool
	DailyUpsertCronSpec        string
	EnableWeeklyUpsertCron     bool
	WeeklyUpsertCronSpec       string
	EnableIdempotencyClearCron bool
	IdempotencyClearCronSpec   string
	InventoryAPIBaseURL        string
	ErrorLogPath               string
	Headless                   bool
	AllowMainDBWrite           bool
	EnableCodexDiscovery       bool
	OpenAIAPIKey               string
	OpenAIModel                string
	PlaywrightCommand          string
	DefaultMaxPages            int
	DefaultMaxScrollAttempts   int
	DefaultMaxLoadMoreClicks   int
	DefaultMaxItems            int
	DataDomeCookie             string
}

func Load() (Config, error) {
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
	setDefaults()

	cfg := Config{
		Port:                       viper.GetString("PORT"),
		ServiceKey:                 viper.GetString("SERVICE_KEY"),
		HMACSecret:                 viper.GetString("HMAC_SECRET"),
		EnableHMAC:                 viper.GetBool("ENABLE_HMAC"),
		RequestBodyLimitMB:         viper.GetInt64("REQUEST_BODY_LIMIT_MB"),
		RateLimitRPS:               viper.GetInt("RATE_LIMIT_RPS"),
		RateLimitBurst:             viper.GetInt("RATE_LIMIT_BURST"),
		SQLitePath:                 viper.GetString("SQLITE_PATH"),
		SiteConfigsDir:             viper.GetString("SITE_CONFIGS_DIR"),
		DefaultRunTimeoutSec:       viper.GetInt("DEFAULT_RUN_TIMEOUT_SEC"),
		Concurrency:                viper.GetInt("SCRAPE_CONCURRENCY"),
		ScrapeMaxAttempts:          viper.GetInt("SCRAPE_MAX_ATTEMPTS"),
		ScrapeRetryBackoffSec:      viper.GetInt("SCRAPE_RETRY_BACKOFF_SEC"),
		HTTPFetchTimeoutSec:        viper.GetInt("HTTP_FETCH_TIMEOUT_SEC"),
		EnableDailyUpsertCron:      viper.GetBool("ENABLE_DAILY_UPSERT_CRON"),
		DailyUpsertCronSpec:        viper.GetString("DAILY_UPSERT_CRON_SPEC"),
		EnableWeeklyUpsertCron:     viper.GetBool("ENABLE_WEEKLY_UPSERT_CRON"),
		WeeklyUpsertCronSpec:       viper.GetString("WEEKLY_UPSERT_CRON_SPEC"),
		EnableIdempotencyClearCron: viper.GetBool("ENABLE_IDEMPOTENCY_CLEAR_CRON"),
		IdempotencyClearCronSpec:   viper.GetString("IDEMPOTENCY_CLEAR_CRON_SPEC"),
		InventoryAPIBaseURL:        viper.GetString("INVENTORY_API_BASE_URL"),
		ErrorLogPath:               viper.GetString("ERROR_LOG_PATH"),
		Headless:                   viper.GetBool("CHROME_HEADLESS"),
		AllowMainDBWrite:           viper.GetBool("ALLOW_MAIN_DB_WRITE"),
		EnableCodexDiscovery:       viper.GetBool("ENABLE_CODEX_DISCOVERY"),
		OpenAIAPIKey:               viper.GetString("OPENAI_API_KEY"),
		OpenAIModel:                viper.GetString("OPENAI_MODEL"),
		PlaywrightCommand:          viper.GetString("PLAYWRIGHT_COMMAND"),
		DefaultMaxPages:            viper.GetInt("DEFAULT_MAX_PAGES"),
		DefaultMaxScrollAttempts:   viper.GetInt("DEFAULT_MAX_SCROLL_ATTEMPTS"),
		DefaultMaxLoadMoreClicks:   viper.GetInt("DEFAULT_MAX_LOAD_MORE_CLICKS"),
		DefaultMaxItems:            viper.GetInt("DEFAULT_MAX_ITEMS"),
		DataDomeCookie:             viper.GetString("DATADOME_COOKIE"),
	}
	if cfg.ServiceKey == "" {
		return Config{}, fmt.Errorf("SERVICE_KEY is required")
	}
	if cfg.SQLitePath == "" {
		return Config{}, fmt.Errorf("SQLITE_PATH is required")
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
	viper.SetDefault("SQLITE_PATH", "data/scraper_results.db")
	viper.SetDefault("SITE_CONFIGS_DIR", "configs/sites")
	viper.SetDefault("DEFAULT_RUN_TIMEOUT_SEC", 600)
	viper.SetDefault("SCRAPE_CONCURRENCY", 4)
	viper.SetDefault("SCRAPE_MAX_ATTEMPTS", 3)
	viper.SetDefault("SCRAPE_RETRY_BACKOFF_SEC", 2)
	viper.SetDefault("HTTP_FETCH_TIMEOUT_SEC", 25)
	viper.SetDefault("ENABLE_DAILY_UPSERT_CRON", false)
	viper.SetDefault("DAILY_UPSERT_CRON_SPEC", "@daily")
	viper.SetDefault("ENABLE_WEEKLY_UPSERT_CRON", false)
	viper.SetDefault("WEEKLY_UPSERT_CRON_SPEC", "0 0 2 * * 0")
	viper.SetDefault("ENABLE_IDEMPOTENCY_CLEAR_CRON", false)
	viper.SetDefault("IDEMPOTENCY_CLEAR_CRON_SPEC", "@daily")
	viper.SetDefault("INVENTORY_API_BASE_URL", "http://localhost")
	viper.SetDefault("ERROR_LOG_PATH", "data/errors.log")
	viper.SetDefault("CHROME_HEADLESS", true)
	viper.SetDefault("ALLOW_MAIN_DB_WRITE", false)
	viper.SetDefault("ENABLE_CODEX_DISCOVERY", false)
	viper.SetDefault("OPENAI_MODEL", "gpt-4.1-mini")
	viper.SetDefault("PLAYWRIGHT_COMMAND", `node -e 'const { chromium } = require("playwright"); (async () => { const browser = await chromium.launch({ headless: true }); const page = await browser.newPage(); await page.goto(process.argv[1], { waitUntil: "networkidle" }); await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight)); await new Promise(r => setTimeout(r, 900)); process.stdout.write(await page.content()); await browser.close(); })().catch((e) => { console.error(e); process.exit(1); });'`)
	viper.SetDefault("DEFAULT_MAX_PAGES", 20)
	viper.SetDefault("DEFAULT_MAX_SCROLL_ATTEMPTS", 8)
	viper.SetDefault("DEFAULT_MAX_LOAD_MORE_CLICKS", 20)
	viper.SetDefault("DEFAULT_MAX_ITEMS", 0)
}
