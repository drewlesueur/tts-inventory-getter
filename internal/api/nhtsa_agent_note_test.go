package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgentNote(t *testing.T) {
	t.Chdir(t.TempDir())
	s, _, _ := evidenceFixture(t)
	h := s.Router()
	request := func(method, body, key, content, path string, unknownLength bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Service-Key", key)
		r.Header.Set("Content-Type", content)
		if unknownLength {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/nhtsa/agent-note"
	ct := "text/plain; charset=utf-8"
	for _, method := range []string{"PUT", "GET"} {
		for _, key := range []string{"", "wrong"} {
			if w := request(method, "secret", key, ct, path, false); w.Code != 401 {
				t.Fatalf("auth: %d", w.Code)
			}
		}
	}
	if w := request("GET", "", "test-key", ct, path, false); w.Code != 404 {
		t.Fatal("missing note")
	}
	for _, body := range []string{"first long message\n", "short ✓", strings.Repeat("x", 8192), ""} {
		w := request("PUT", body, "test-key", ct, path, false)
		var result struct {
			Success   bool
			Bytes     int
			UpdatedAt time.Time
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Success || result.Bytes != len(body) || result.UpdatedAt.IsZero() {
			t.Fatalf("PUT: %d %s", w.Code, w.Body)
		}
		got := request("GET", "", "test-key", ct, path, false)
		if got.Code != 200 || got.Body.String() != body || got.Header().Get("Content-Type") != ct {
			t.Fatal("readback differs")
		}
	}
	request("PUT", "preserve me", "test-key", ct, path, false)
	for _, tc := range []struct {
		body, content, path string
		unknown             bool
		status              int
	}{
		{strings.Repeat("x", 8193), ct, path, false, 413},
		{strings.Repeat("x", 8193), ct, path, true, 413},
		{string([]byte{255}), ct, path, false, 400},
		{"new", "application/json", path, false, 415},
		{"new", ct, path + "?filename=other", false, 400},
	} {
		if w := request("PUT", tc.body, "test-key", tc.content, tc.path, tc.unknown); w.Code != tc.status {
			t.Fatalf("rejection: %d want %d", w.Code, tc.status)
		}
		b, err := readInstruction()
		if err != nil || b.Message != "preserve me" {
			t.Fatal("rejected write modified note")
		}
	}
	info, err := os.Stat(agentNotePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("file permissions")
	}
	files, _ := os.ReadDir("diagnostics")
	if len(files) != 1 {
		t.Fatal("temporary files leaked")
	}
	if w := request("POST", "", "test-key", ct, path, false); w.Code != 405 {
		t.Fatal("POST accepted")
	}
	if w := request("HEAD", "", "test-key", ct, path, false); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD failed")
	}
}

func TestAgentResponseCorrelationAndLimits(t *testing.T) {
	t.Chdir(t.TempDir())
	s, _, _ := evidenceFixture(t)
	h := s.Router()
	get := func() *httptest.ResponseRecorder {
		return evidenceRequest(h, "GET", "/v1/nhtsa/agent-response", "test-key")
	}
	if w := get(); w.Code != 404 {
		t.Fatal("expected no instruction")
	}
	if w := evidenceRequest(h, "GET", "/v1/nhtsa/agent-response", ""); w.Code != 401 {
		t.Fatal("response authentication")
	}
	put := func() string {
		r := httptest.NewRequest("PUT", "/v1/nhtsa/agent-note", strings.NewReader("inspect only"))
		r.Header.Set("X-Service-Key", "test-key")
		r.Header.Set("Content-Type", "text/plain; charset=utf-8")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("PUT failed")
		}
		return result["messageId"].(string)
	}
	check := func(id, status, text string) {
		t.Helper()
		w := get()
		var result map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result["messageId"] != id || result["status"] != status || result["response"] != text {
			t.Fatalf("unexpected response: %d", w.Code)
		}
	}
	save := func(id, text string) {
		t.Helper()
		b, _ := json.Marshal(agentReply{MessageID: id, Response: text})
		if _, err := replaceBridgeFile(agentResponsePath, b); err != nil {
			t.Fatal(err)
		}
	}
	first := put()
	check(first, "pending", "")
	save(first, "Complete answer\n✓")
	check(first, "completed", "Complete answer\n✓")
	second := put()
	if first == second {
		t.Fatal("ID reused")
	}
	check(second, "pending", "")
	// A late reply to the superseded instruction must remain invisible.
	save(first, "stale reply")
	check(second, "pending", "")
	// Both decoded and encoded byte limits matter; escaped characters are supported.
	large := strings.Repeat("\x01", 65536)
	save(second, large)
	check(second, "completed", large)
	save(second, strings.Repeat("x", 65537))
	if w := get(); w.Code != 503 || !strings.Contains(w.Body.String(), "RESPONSE_TOO_LARGE") {
		t.Fatal("oversized response was not rejected")
	}
	os.WriteFile(agentResponsePath, []byte(`{"messageId":"`+second+`"}`), 0600)
	if w := get(); w.Code != 503 {
		t.Fatal("missing response accepted")
	}
	os.WriteFile(agentResponsePath, []byte{255}, 0600)
	if w := get(); w.Code != 503 {
		t.Fatal("invalid UTF-8 accepted")
	}
	save(second, "final")
	h = s.Router()
	check(second, "completed", "final")
	if w := evidenceRequest(h, "PUT", "/v1/nhtsa/agent-response", "test-key"); w.Code != 405 {
		t.Fatal("remote response write accepted")
	}
}
