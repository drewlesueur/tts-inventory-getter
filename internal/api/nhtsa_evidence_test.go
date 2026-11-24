package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
)

func evidenceFixture(t *testing.T) (*Server, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	diagnostic := []byte(`{"vin":"1FMUK7DHXSGA27345","status":400}`)
	var p bytes.Buffer
	if err := png.Encode(&p, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	hash := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	m, _ := json.Marshal(nhtsaEvidenceManifest{Version: 1, CapturedAt: time.Date(2026, 10, 13, 21, 7, 37, 0, time.UTC), RedactionReviewed: true, JSONSHA256: hash(diagnostic), ScreenshotSHA256: hash(p.Bytes())})
	for name, data := range map[string][]byte{"manifest.json": m, "latest.json": diagnostic, "latest.png": p.Bytes()} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// No store or scraper configured: retrieval must work independently of both.
	return &Server{cfg: config.Config{ServiceKey: "test-key", NHTSADiagnosticsDir: dir, RateLimitRPS: 1000, RateLimitBurst: 1000, RequestBodyLimitMB: 1}}, diagnostic, p.Bytes()
}
func evidenceRequest(h http.Handler, method, path, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-Service-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestEvidenceRetrieval(t *testing.T) {
	s, j, p := evidenceFixture(t)
	h := s.Router()
	for _, tc := range []struct {
		path, mime string
		data       []byte
	}{{"/v1/nhtsa/diagnostics/latest", "application/json", j}, {"/v1/nhtsa/diagnostics/latest/screenshot", "image/png", p}} {
		for i := 0; i < 2; i++ {
			w := evidenceRequest(h, "GET", tc.path, "test-key")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), tc.data) || w.Header().Get("Content-Type") != tc.mime {
				t.Fatalf("retrieval failed: %d", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Evidence-Captured-At") == "" {
				t.Fatal("missing evidence headers")
			}
		}
		for _, key := range []string{"", "wrong"} {
			if w := evidenceRequest(h, "GET", tc.path, key); w.Code != 401 {
				t.Fatalf("unauthenticated: %d", w.Code)
			}
		}
		if w := evidenceRequest(h, "HEAD", tc.path, "test-key"); w.Code != 200 || w.Body.Len() != 0 {
			t.Fatal("HEAD failed")
		}
		if w := evidenceRequest(h, "POST", tc.path, "test-key"); w.Code != 405 {
			t.Fatalf("POST accepted: %d", w.Code)
		}
		if w := evidenceRequest(h, "GET", tc.path+"?refresh=true", "test-key"); w.Code != 400 {
			t.Fatal("query accepted")
		}
	}
}
func TestEvidenceFailsClosed(t *testing.T) {
	for _, kind := range []string{"missing", "changed", "unreviewed", "oversize", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := evidenceFixture(t)
			dir := s.cfg.NHTSADiagnosticsDir
			want := 503
			switch kind {
			case "missing":
				os.Remove(filepath.Join(dir, "latest.png"))
				want = 404
			case "changed":
				os.WriteFile(filepath.Join(dir, "latest.json"), []byte(`{"token":"must-not-leak"}`), 0600)
			case "unreviewed":
				os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"version":1,"redactionReviewed":false}`), 0600)
			case "oversize":
				os.WriteFile(filepath.Join(dir, "latest.json"), bytes.Repeat([]byte("x"), 257<<10), 0600)
			case "symlink":
				os.Rename(filepath.Join(dir, "latest.json"), filepath.Join(dir, "other.json"))
				os.Symlink(filepath.Join(dir, "other.json"), filepath.Join(dir, "latest.json"))
			}
			for _, path := range []string{"/v1/nhtsa/diagnostics/latest", "/v1/nhtsa/diagnostics/latest/screenshot"} {
				w := evidenceRequest(s.Router(), "GET", path, "test-key")
				if w.Code != want || bytes.Contains(w.Body.Bytes(), []byte("must-not-leak")) {
					t.Fatalf("unsafe response: %d", w.Code)
				}
			}
		})
	}
}
func TestEvidenceHMAC(t *testing.T) {
	s, _, _ := evidenceFixture(t)
	s.cfg.EnableHMAC = true
	s.cfg.HMACSecret = "test-secret"
	h := s.Router()
	if w := evidenceRequest(h, "GET", "/v1/nhtsa/diagnostics/latest", "test-key"); w.Code != 401 {
		t.Fatal("HMAC not enforced")
	}
	r := httptest.NewRequest("GET", "/v1/nhtsa/diagnostics/latest", nil)
	r.Header.Set("X-Service-Key", "test-key")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(ts))
	r.Header.Set("X-Request-Timestamp", ts)
	r.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("signed GET: %d", w.Code)
	}
}
