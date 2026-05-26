package scrape

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

type CronRunner struct {
	cron *cron.Cron
}

func StartCron(logger *zap.Logger, spec string, job func()) (*CronRunner, error) {
	return StartCronNamed(logger, "", spec, job)
}

func StartCronNamed(logger *zap.Logger, name, spec string, job func()) (*CronRunner, error) {
	c := cron.New(cron.WithSeconds())
	wrapped := func() {
		start := time.Now()
		logger.Info("cron tick started", zap.String("name", name), zap.String("spec", spec))
		defer func() {
			if r := recover(); r != nil {
				logger.Error("cron tick panicked", zap.String("name", name), zap.Any("panic", r))
			}
		}()
		job()
		logger.Info("cron tick finished", zap.String("name", name), zap.Duration("elapsed", time.Since(start)))
	}
	entryID, err := c.AddFunc(spec, wrapped)
	if err != nil {
		return nil, err
	}
	c.Start()
	next := time.Time{}
	if entry := c.Entry(entryID); entry.ID != 0 {
		next = entry.Next
	}
	logger.Info("cron started", zap.String("name", name), zap.String("spec", spec), zap.Time("nextRun", next))
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
