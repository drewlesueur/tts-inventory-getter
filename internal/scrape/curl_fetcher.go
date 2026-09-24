package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CurlFetcher delegates HTTP fetches to the Python curl_cffi script which
// provides true Chrome-level TLS + HTTP/2 fingerprint impersonation.
// This is needed for DataDome-protected sites where Go's TLS client gets rejected.
type CurlFetcher struct {
	ScriptPath  string // path to fetch_page.py
	PythonBin   string // e.g. "python3.11" or "python3"
	CookieStore *CookieStore
	// Fallback is a plain HTTP fetcher used when the Python subprocess can't run
	// (e.g. wrong PYTHON_BIN) or errors. Non-DataDome sites scrape fine via plain
	// HTTP, so a Python misconfig should not break them.
	Fallback Fetcher
}

func NewCurlFetcher(scriptPath, pythonBin string, store *CookieStore) *CurlFetcher {
	pythonBin = resolvePythonBin(pythonBin)
	return &CurlFetcher{ScriptPath: scriptPath, PythonBin: pythonBin, CookieStore: store}
}

type fetchPageResult struct {
	HTML   string `json:"html"`
	Cookie string `json:"cookie"`
	Status int    `json:"status"`
	Error  string `json:"error"`
}

func (c *CurlFetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	return c.fetchWithCookie(ctx, rawURL, "")
}

func (c *CurlFetcher) FetchWithCookie(ctx context.Context, rawURL, cookieHeader string) (string, error) {
	return c.fetchWithCookie(ctx, rawURL, datadomeFromHeader(cookieHeader))
}

// datadomeFromHeader pulls the datadome value out of a "a=1; b=2" cookie header.
func datadomeFromHeader(cookieHeader string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "datadome=") {
			return strings.TrimPrefix(part, "datadome=")
		}
	}
	return ""
}

// FetchRendered re-fetches a page with the fast HTTP path disabled, forcing the
// Python layer into a real browser. Client-rendered inventory (DealerCenter's
// dws-* widgets) answers plain HTTP with a card-less shell that looks like a
// success, so a shell is only worth retrying in a browser that can hydrate it.
func (c *CurlFetcher) FetchRendered(ctx context.Context, rawURL, cookieHeader string) (string, error) {
	// Hydrating a client-rendered SRP in a real browser takes far longer than a
	// plain HTTP GET, so don't inherit a deadline sized for the latter.
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) < renderedFetchTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), renderedFetchTimeout)
		defer cancel()
	}
	return c.fetchWithCookieOpts(ctx, rawURL, datadomeFromHeader(cookieHeader), true)
}

// renderedFetchTimeout covers a Cloudflare challenge (~80s) plus hydration.
const renderedFetchTimeout = 180 * time.Second

func (c *CurlFetcher) fetchWithCookie(ctx context.Context, rawURL, cookie string) (string, error) {
	return c.fetchWithCookieOpts(ctx, rawURL, cookie, false)
}

func (c *CurlFetcher) fetchWithCookieOpts(ctx context.Context, rawURL, cookie string, skipHTTP bool) (string, error) {
	args := []string{c.ScriptPath, rawURL}
	if cookie != "" {
		args = append(args, cookie)
	}

	cmd := exec.CommandContext(ctx, c.PythonBin, args...)
	if skipHTTP {
		cmd.Env = append(os.Environ(), "FETCH_SKIP_HTTP=1")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// In rendered mode the plain-HTTP fallback is worse than useless: the
	// caller escalated precisely because HTTP returned a card-less shell, and
	// falling back hands that same shell back as a success.
	onErr := func(err error) (string, error) {
		if skipHTTP {
			return "", err
		}
		return c.fallback(ctx, rawURL, err)
	}

	runErr := cmd.Run()
	if runErr != nil {
		return onErr(fmt.Errorf("curl_cffi exec failed: %w — %s", runErr, strings.TrimSpace(stderr.String())))
	}

	var result fetchPageResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return onErr(fmt.Errorf("curl_cffi output parse failed: %w", err))
	}
	if result.Error != "" {
		return onErr(fmt.Errorf("curl_cffi: %s", result.Error))
	}
	if result.Status >= 400 {
		return onErr(fmt.Errorf("curl_cffi fetch failed status=%d", result.Status))
	}

	// Save refreshed cookie back to the store
	if result.Cookie != "" && c.CookieStore != nil {
		_ = c.CookieStore.Set("datadome", result.Cookie)
	}

	return result.HTML, nil
}

// fallback tries the plain HTTP fetcher when the Python path fails. If the
// fallback also fails or returns a DataDome challenge, the original error is
// returned so the caller still sees the real reason.
func (c *CurlFetcher) fallback(ctx context.Context, rawURL string, origErr error) (string, error) {
	if c.Fallback == nil {
		return "", origErr
	}
	html, err := c.Fallback.Fetch(ctx, rawURL)
	if err != nil || isDataDomeChallenge(html) {
		return "", origErr
	}
	return html, nil
}
