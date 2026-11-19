package scrape

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "net/url"
    "net/http"
    "regexp"
    "runtime/debug"
    "strings"
    "sync"
    "time"

    "github.com/PuerkitoBio/goquery"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
)

const diagnosticLimit = 160
const diagnosticMessageLimit = 3000

type diagnosticContextKey struct{}
type diagnosticAttemptKey struct{}
type Diagnostics struct {
    mu sync.Mutex
    events []model.DiagnosticEvent
    dropped int
    attempt int
}

// WithDiagnostics carries only per-run state. It is safe for concurrent detail
// fetches and bounded independently of page count and subprocess output size.
func WithDiagnostics(ctx context.Context) (context.Context, *Diagnostics) {
    if d, ok := ctx.Value(diagnosticContextKey{}).(*Diagnostics); ok { return ctx, d }
    d := &Diagnostics{}
    return context.WithValue(ctx, diagnosticContextKey{}, d), d
}

func (d *Diagnostics) SetAttempt(attempt int) { d.mu.Lock(); d.attempt = attempt; d.mu.Unlock() }

func (d *Diagnostics) Add(event model.DiagnosticEvent) {
    if d == nil { return }
    event.At = time.Now().UTC().Format(time.RFC3339Nano)
    event.URL = DiagnosticURL(event.URL)
    event.FinalURL = DiagnosticURL(event.FinalURL)
    event.Message = DiagnosticText(event.Message)
    event.Title = DiagnosticText(event.Title)
    if len(event.Title) > 200 { event.Title = event.Title[:200] }
    event.Method = DiagnosticText(event.Method)
    if len(event.Method)>80 { event.Method=event.Method[:80] }
    event.Stage = DiagnosticText(event.Stage)
    event.BlockMarker = DiagnosticText(event.BlockMarker)
    if len(event.Stage)>80 { event.Stage=event.Stage[:80] }
    if len(event.BlockMarker)>100 { event.BlockMarker=event.BlockMarker[:100] }
    if len(event.BodySHA256)>64 { event.BodySHA256=event.BodySHA256[:64] }
    d.mu.Lock()
    defer d.mu.Unlock()
    if event.Attempt == 0 { event.Attempt = d.attempt }
    // Keep the opening context and the most recent evidence, including the
    // final failure. Do not lose the outcome when a large inventory hits the cap.
    if len(d.events) >= diagnosticLimit {
        copy(d.events[16:], d.events[17:])
        d.events = d.events[:diagnosticLimit-1]
        d.dropped++
    }
    d.events = append(d.events, event)
}

func (d *Diagnostics) Snapshot() *model.Diagnostics {
    if d == nil { return nil }
    d.mu.Lock(); defer d.mu.Unlock()
    events := append([]model.DiagnosticEvent(nil), d.events...)
    return &model.Diagnostics{Version: diagnosticVersion(), Events: events, DroppedEvents: d.dropped}
}

var diagnosticBuildVersion string
var diagnosticBuildVersionOnce sync.Once

func diagnosticVersion() string {
    diagnosticBuildVersionOnce.Do(func(){diagnosticBuildVersion=readDiagnosticVersion()})
    return diagnosticBuildVersion
}

func readDiagnosticVersion() string {
    version := "unknown"
    if info, ok := debug.ReadBuildInfo(); ok {
        if info.Main.Version != "" && info.Main.Version != "(devel)" { version = info.Main.Version }
        for _, setting := range info.Settings {
            if setting.Key == "vcs.revision" { version = setting.Value }
        }
        for _, setting := range info.Settings {
            if setting.Key == "vcs.modified" && setting.Value == "true" { version += "+modified" }
        }
    }
    return version
}

// Capture the attempt in the context: a timed-out worker can finish after the
// next attempt starts, and must not be attributed to that later attempt.
func WithDiagnosticAttempt(ctx context.Context, attempt int) context.Context {
    return context.WithValue(ctx, diagnosticAttemptKey{}, attempt)
}

func RecordDiagnostic(ctx context.Context, event model.DiagnosticEvent) {
    if attempt, ok := ctx.Value(diagnosticAttemptKey{}).(int); ok { event.Attempt = attempt }
    if d, ok := ctx.Value(diagnosticContextKey{}).(*Diagnostics); ok { d.Add(event) }
}

var diagnosticURLPattern = regexp.MustCompile(`(?i)(?:https?|socks5?|ftp)://[^\s<>"']+`)
var diagnosticBearerPattern = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)
var diagnosticSecretPattern = regexp.MustCompile(`(?i)(authorization|proxy-authorization|cookie|set-cookie|api[_-]?key|service[_-]?key|access[_-]?token|refresh[_-]?token|token|datadome|password|secret)(["']?\s*[:=]\s*)([^\r\n]+)`)

func DiagnosticURL(raw string) string {
    if raw == "" { return "" }
    u, err := url.Parse(raw)
    if err != nil || u.Host == "" { return "[invalid URL]" }
    u.User = nil; u.RawQuery = ""; u.ForceQuery = false; u.Fragment = ""; u.RawFragment = ""
    result := u.String()
    if len(result) > 600 { result = result[:600] }
    return result
}

func DiagnosticText(raw string) string {
    raw = diagnosticURLPattern.ReplaceAllStringFunc(raw, DiagnosticURL)
    raw = diagnosticSecretPattern.ReplaceAllString(raw, "$1$2[redacted]")
    raw = diagnosticBearerPattern.ReplaceAllString(raw,"Bearer [redacted]")
    if len(raw) > diagnosticMessageLimit { raw = raw[:diagnosticMessageLimit] + " [truncated]" }
    return raw
}

func recordPageDiagnostic(ctx context.Context, stage, method, rawURL, finalURL, html string, status int, started time.Time, err error) {
    if _, ok := ctx.Value(diagnosticContextKey{}).(*Diagnostics); !ok { return }
    event := model.DiagnosticEvent{Stage:stage, Method:method, URL:rawURL, FinalURL:finalURL, HTTPStatus:status, DurationMS:time.Since(started).Milliseconds(), Bytes:len(html)}
    if html != "" {
        sum := sha256.Sum256([]byte(html)); event.BodySHA256 = hex.EncodeToString(sum[:])
        // Inspect a bounded prefix; do not persist HTML, cookies, or form data.
        sample := html; if len(sample) > 256*1024 { sample = sample[:256*1024] }
        if doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(sample)); parseErr == nil { event.Title = strings.TrimSpace(doc.Find("title").First().Text()) }
        lower := strings.ToLower(sample)
        for _, marker := range []string{"sorry, you have been blocked", "cf-chl-", "captcha-delivery.com", "access denied", "just a moment", "verify you are human"} {
            if strings.Contains(lower, marker) { event.BlockMarker = marker; break }
        }
    }
    if err != nil { event.Message = err.Error() }
    RecordDiagnostic(ctx, event)
}

// boundedDiagnosticBuffer retains the tail of stderr instead of buffering an
// unlimited subprocess stream. Write always accepts the original byte count.
type boundedDiagnosticBuffer struct { data []byte; truncated bool }
func (b *boundedDiagnosticBuffer) Write(p []byte) (int, error) {
    n := len(p); const limit = 16384
    if len(p) >= limit { b.data = append(b.data[:0], p[len(p)-limit:]...); b.truncated=true; return n, nil }
    excess := len(b.data)+len(p)-limit
    if excess > 0 { b.data = b.data[excess:]; b.truncated=true }
    b.data = append(b.data,p...)
    return n,nil
}
func (b *boundedDiagnosticBuffer) String() string {
    output:=string(b.data)
    if b.truncated {
        // Discard a partial first line: its secret field name may have been
        // truncated, leaving a value that the redactor could not identify.
        if newline:=strings.IndexByte(output,'\n');newline>=0 {output=output[newline+1:]} else {output=""}
        return "[earlier stderr truncated]\n"+output
    }
    return output
}

func recordErrorDiagnostic(ctx context.Context, stage, method, rawURL string, started time.Time, err error) {
    recordPageDiagnostic(ctx,stage,method,rawURL,"","",0,started,err)
}

type diagnosticWrappedError struct { cause error; message string }
func (e diagnosticWrappedError) Error() string { return e.message }
func (e diagnosticWrappedError) Unwrap() error { return e.cause }

func diagnosticError(err error, stderr string) error {
    message := DiagnosticText(err.Error())
    if strings.TrimSpace(stderr) != "" { message += "; fetcher diagnostics: " + DiagnosticText(stderr) }
    return diagnosticWrappedError{cause:err,message:message}
}

func recordSubprocessDiagnostics(ctx context.Context, method, rawURL, output string) {
    for _, line := range strings.Split(output,"\n") {
        if !strings.HasPrefix(line,"SCRAPE_DIAGNOSTIC ") { continue }
        var event model.DiagnosticEvent
        if json.Unmarshal([]byte(strings.TrimPrefix(line,"SCRAPE_DIAGNOSTIC ")),&event)==nil { RecordDiagnostic(ctx,event) }
    }
    if output!="" { RecordDiagnostic(ctx,model.DiagnosticEvent{Stage:"fetcher_output",Method:method,URL:rawURL,Message:output}) }
}

// Observe native HTTP adapters without changing requests, retries, redirects or
// response bodies. Authentication headers and bodies are deliberately omitted.
func diagnosticHTTPDo(client *http.Client, req *http.Request) (*http.Response,error) {
    started:=time.Now()
    resp,err:=client.Do(req)
    event:=model.DiagnosticEvent{Stage:"http_response",Method:"native_http",URL:req.URL.String(),DurationMS:time.Since(started).Milliseconds()}
    if resp!=nil {
        event.HTTPStatus=resp.StatusCode
        if resp.Request!=nil && resp.Request.URL!=nil {event.FinalURL=resp.Request.URL.String()}
    }
    if err!=nil {event.Message=err.Error()}
    RecordDiagnostic(req.Context(),event)
    return resp,err
}
