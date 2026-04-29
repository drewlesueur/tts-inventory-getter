package metrics

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	RunsTotal    *prometheus.CounterVec
	RunDuration  *prometheus.HistogramVec
	ItemsScraped *prometheus.CounterVec
}

func New() *Metrics {
	m := &Metrics{
		RunsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scraper_runs_total", Help: "Total scrape runs"}, []string{"status"}),
		RunDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "scraper_run_duration_seconds", Help: "Run duration", Buckets: prometheus.DefBuckets}, []string{"dealership"}),
		ItemsScraped: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scraper_items_total", Help: "Total items scraped"}, []string{"dealership"}),
	}
	prometheus.MustRegister(m.RunsTotal, m.RunDuration, m.ItemsScraped)
	return m
}
