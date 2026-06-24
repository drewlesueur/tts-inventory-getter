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

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type HTTPFetcher struct {
	client       *http.Client
	unsafeClient *http.Client
	CookieStore  *CookieStore
	ProxyURL     string // e.g. "http://user:pass@host:port" or "socks5://..."
}

func NewHTTPFetcher() *HTTPFetcher {
	return NewHTTPFetcherWithTimeout(25 * time.Second)
}

func NewHTTPFetcherWithTimeout(requestTimeout time.Duration) *HTTPFetcher {
	if requestTimeout <= 0 {
		requestTimeout = 25 * time.Second
	}
	client := &http.Client{
		Timeout: requestTimeout,
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
		Timeout: requestTimeout,
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

// FetchWithCookie uses a Chrome-impersonating TLS client so that DataDome
// validates the cookie against a matching TLS fingerprint.
func (f *HTTPFetcher) FetchWithCookie(ctx context.Context, rawURL, cookie string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if err := rejectUnsafeURL(u); err != nil {
		return "", err
	}

	tlsOpts := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_120),
	}
	if f.ProxyURL != "" {
		tlsOpts = append(tlsOpts, tls_client.WithProxyUrl(f.ProxyURL))
	}
	tlsClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), tlsOpts...)
	if err != nil {
		return "", fmt.Errorf("tls client init: %w", err)
	}

	req, err := fhttp.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header = fhttp.Header{
		"User-Agent":                []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
		"Accept":                    []string{"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"},
		"Accept-Language":           []string{"en-US,en;q=0.9"},
		"Cookie":                    []string{cookie},
		"sec-fetch-dest":            []string{"document"},
		"sec-fetch-mode":            []string{"navigate"},
		"sec-fetch-site":            []string{"none"},
		"upgrade-insecure-requests": []string{"1"},
	}

	type result struct {
		body      string
		newCookie string
		err       error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := tlsClient.Do(req)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			ch <- result{err: fmt.Errorf("fetch failed status=%d", resp.StatusCode)}
			return
		}
		// Capture refreshed datadome cookie from response headers.
		var newCookie string
		for _, sc := range resp.Header["Set-Cookie"] {
			if strings.HasPrefix(sc, "datadome=") {
				newCookie = strings.TrimPrefix(strings.SplitN(sc, ";", 2)[0], "datadome=")
				break
			}
		}
		b, err := io.ReadAll(resp.Body)
		ch <- result{body: string(b), newCookie: newCookie, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.err == nil && r.newCookie != "" && f.CookieStore != nil {
			_ = f.CookieStore.Set("datadome", r.newCookie)
		}
		return r.body, r.err
	}
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// NOTE: do not set Accept-Encoding manually — Go's transport then stops
	// auto-decompressing the response, leaving goquery to parse compressed bytes.
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
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
