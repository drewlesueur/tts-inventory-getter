package scrape

import (
	"context"
	"math"
	"time"
)

func Retry(ctx context.Context, attempts int, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil { return nil }
		backoff := time.Duration(math.Pow(2, float64(i))) * 200 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return err
}
