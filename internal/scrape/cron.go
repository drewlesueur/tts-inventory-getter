package scrape

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/example/inventory-scraper/internal/config"
)

type CronRunner struct {
	cron *cron.Cron
}

func StartCron(logger *zap.Logger, spec string, job func()) (*CronRunner, error) {
	c := cron.New(cron.WithSeconds())
	_, err := c.AddFunc(spec, job)
	if err != nil { return nil, err }
	c.Start()
	logger.Info("cron started", zap.String("spec", spec))
	return &CronRunner{cron: c}, nil
}

func (r *CronRunner) Stop(ctx context.Context) {
	ch := r.cron.Stop().Done()
	select {
	case <-ch:
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

func DefaultCronSpec(prod bool) string {
	if prod { return "0 0 2 * * *" }
	return "0 */5 * * * *"
}

func BuildCronJob(_ config.Config) func() {
	return func() {}
}
