package scrape

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPFetcher struct {
	client       *http.Client
	unsafeClient *http.Client
}

func NewHTTPFetcher() *HTTPFetcher {
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := rejectUnsafeURL(req.URL); err != nil {
				return err
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after too many redirects")
			}
			return nil
		},
	}
	unsafeClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after too many redirects")
			}
			return nil
		},
	}
	return &HTTPFetcher{client: client, unsafeClient: unsafeClient}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (string, error) {
	return f.fetchWithClient(ctx, url, f.client, true)
}

func (f *HTTPFetcher) FetchUnsafe(ctx context.Context, url string) (string, error) {
	return f.fetchWithClient(ctx, url, f.unsafeClient, false)
}

func (f *HTTPFetcher) fetchWithClient(ctx context.Context, rawURL string, client *http.Client, enforceSafe bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if enforceSafe {
		if err := rejectUnsafeURL(req.URL); err != nil {
			return "", err
		}
	}
	req.Header.Set("User-Agent", "inventory-scraper/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch failed status=%d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func rejectUnsafeURL(u *url.URL) error {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("empty host in url")
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return fmt.Errorf("blocked redirect to local host: %s", u.String())
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("blocked redirect to private host: %s", u.String())
		}
	}
	return nil
}
