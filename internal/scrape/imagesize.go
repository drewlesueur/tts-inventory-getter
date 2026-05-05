package scrape

import (
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync"
	"time"
)

const imageProbeBytes = 64 * 1024

type ImageSizeCache struct {
	mu sync.RWMutex
	m  map[string]imageDims
}

type imageDims struct{ w, h int }

func NewImageSizeCache() *ImageSizeCache {
	return &ImageSizeCache{m: map[string]imageDims{}}
}

func (c *ImageSizeCache) get(u string) (int, int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.m[u]
	return d.w, d.h, ok
}

func (c *ImageSizeCache) set(u string, w, h int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[u] = imageDims{w, h}
}

func probeImageSize(ctx context.Context, client *http.Client, url string) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", imageProbeBytes-1))
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ImageSizeProbe/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	cfg, _, err := image.DecodeConfig(io.LimitReader(resp.Body, imageProbeBytes))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func filterByImageSize(ctx context.Context, cache *ImageSizeCache, urls []string, minDim int) []string {
	if len(urls) == 0 || cache == nil {
		return urls
	}
	client := &http.Client{Timeout: 10 * time.Second}
	keep := make([]bool, len(urls))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, u := range urls {
		if w, h, ok := cache.get(u); ok {
			keep[i] = w >= minDim && h >= minDim
			continue
		}
		i, u := i, u
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			pctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			w, h, err := probeImageSize(pctx, client, u)
			if err != nil {
				keep[i] = true
				return
			}
			cache.set(u, w, h)
			keep[i] = w >= minDim && h >= minDim
		}()
	}
	wg.Wait()
	out := make([]string, 0, len(urls))
	for i, u := range urls {
		if keep[i] {
			out = append(out, u)
		}
	}
	return out
}
