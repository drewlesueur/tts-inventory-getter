package scrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
	// cookieHeader is "name=value; name2=value2" — extract the datadome value
	cookie := ""
	for _, part := range strings.Split(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "datadome=") {
			cookie = strings.TrimPrefix(part, "datadome=")
			break
		}
	}
	return c.fetchWithCookie(ctx, rawURL, cookie)
}

func (c *CurlFetcher) fetchWithCookie(ctx context.Context, rawURL, cookie string) (string, error) {
	args := []string{c.ScriptPath, rawURL}
	if cookie != "" {
		args = append(args, cookie)
	}

	cmd := exec.CommandContext(ctx, c.PythonBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr != nil {
		return c.fallback(ctx, rawURL, fmt.Errorf("curl_cffi exec failed: %w — %s", runErr, strings.TrimSpace(stderr.String())))
	}

	var result fetchPageResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return c.fallback(ctx, rawURL, fmt.Errorf("curl_cffi output parse failed: %w", err))
	}
	if result.Error != "" {
		return c.fallback(ctx, rawURL, fmt.Errorf("curl_cffi: %s", result.Error))
	}
	if result.Status >= 400 {
		return c.fallback(ctx, rawURL, fmt.Errorf("curl_cffi fetch failed status=%d", result.Status))
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
