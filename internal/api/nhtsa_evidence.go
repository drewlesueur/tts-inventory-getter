package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

// The manifest pins a manually reviewed, redacted pair. These handlers only
// read saved files: no scraper, browser, job creation, or agent invocation.
type nhtsaEvidenceManifest struct {
	Version           int       `json:"version"`
	CapturedAt        time.Time `json:"capturedAt"`
	RedactionReviewed bool      `json:"redactionReviewed"`
	JSONSHA256        string    `json:"jsonSha256"`
	ScreenshotSHA256  string    `json:"screenshotSha256"`
}

func readEvidenceFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid evidence file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("evidence too large")
	}
	return data, nil
}
func matchesEvidenceHash(data []byte, expected string) bool {
	sum := sha256.Sum256(data)
	return len(expected) == 64 && hex.EncodeToString(sum[:]) == expected
}

func (s *Server) handleNHTSAEvidence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// No request-controlled paths, file names, or refresh switches.
	if r.URL.RawQuery != "" {
		writeJSON(w, 400, model.ErrorResponse("INVALID_REQUEST", "saved evidence endpoints do not accept query parameters"))
		return
	}
	dir := s.cfg.NHTSADiagnosticsDir
	if dir == "" {
		dir = "data/nhtsa-diagnostics"
	}
	fail := func(err error) {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, 404, model.ErrorResponse("EVIDENCE_NOT_FOUND", "no reviewed diagnostic evidence is available"))
			return
		}
		writeJSON(w, 503, model.ErrorResponse("EVIDENCE_UNAVAILABLE", "saved evidence could not be verified"))
	}
	raw, err := readEvidenceFile(filepath.Join(dir, "manifest.json"), 4096)
	if err != nil {
		fail(err)
		return
	}
	var manifest nhtsaEvidenceManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || !manifest.RedactionReviewed || manifest.CapturedAt.IsZero() {
		fail(errors.New("invalid manifest"))
		return
	}
	diagnostic, err := readEvidenceFile(filepath.Join(dir, "latest.json"), 256<<10)
	if err != nil {
		fail(err)
		return
	}
	screenshot, err := readEvidenceFile(filepath.Join(dir, "latest.png"), 8<<20)
	if err != nil {
		fail(err)
		return
	}
	if !json.Valid(diagnostic) || !matchesEvidenceHash(diagnostic, manifest.JSONSHA256) || !matchesEvidenceHash(screenshot, manifest.ScreenshotSHA256) || !bytes.HasPrefix(screenshot, []byte("\x89PNG\r\n\x1a\n")) {
		fail(errors.New("invalid saved evidence"))
		return
	}
	w.Header().Set("X-Evidence-Captured-At", manifest.CapturedAt.UTC().Format(time.RFC3339Nano))
	w.Header().Set("Last-Modified", manifest.CapturedAt.UTC().Format(http.TimeFormat))
	data, mime, name, hash := diagnostic, "application/json", "nhtsa-diagnostic.json", manifest.JSONSHA256
	if r.URL.Path == "/v1/nhtsa/diagnostics/latest/screenshot" {
		data, mime, name, hash = screenshot, "image/png", "nhtsa-diagnostic.png", manifest.ScreenshotSHA256
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	w.Header().Set("ETag", `"`+hash+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
