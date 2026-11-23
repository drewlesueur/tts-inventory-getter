package api

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
)

const agentNotePath = "diagnostics/taptosign-agent-note.txt"
const agentNoteLimit = 8 * 1024
const agentResponsePath = "diagnostics/lightsail-agent-response.txt"
const agentResponseLimit = 64 * 1024

var agentBridgeMu sync.Mutex

type agentInstruction struct {
	MessageID string `json:"messageId"`
	Message   string `json:"message"`
}
type agentReply struct {
	MessageID string `json:"messageId"`
	Response  string `json:"response"`
}

func readInstruction() (agentInstruction, error) {
	var note agentInstruction
	b, err := readEvidenceFile(agentNotePath, 6*agentNoteLimit+4096)
	if err != nil {
		return note, err
	}
	if !utf8.Valid(b) || json.Unmarshal(b, &note) != nil || note.MessageID == "" || len(note.Message) > agentNoteLimit {
		return note, errors.New("invalid instruction")
	}
	return note, nil
}

// This is passive text storage. Nothing reads this file for execution or jobs.
func (s *Server) handleAgentNote(w http.ResponseWriter, r *http.Request) {
	agentBridgeMu.Lock()
	defer agentBridgeMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int, code, message string) { writeJSON(w, status, model.ErrorResponse(code, message)) }
	if r.URL.RawQuery != "" {
		fail(400, "INVALID_REQUEST", "query parameters are not accepted")
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		note, err := readInstruction()
		if errors.Is(err, os.ErrNotExist) {
			fail(404, "NOTE_NOT_FOUND", "no agent note has been saved")
			return
		}
		if err != nil {
			fail(503, "NOTE_UNAVAILABLE", "saved note could not be read")
			return
		}
		w.Header().Set("X-Message-Id", note.MessageID)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(note.Message))
		}
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "text/plain" || !strings.EqualFold(params["charset"], "utf-8") {
		fail(415, "UNSUPPORTED_MEDIA_TYPE", "use text/plain; charset=utf-8")
		return
	}
	if r.ContentLength > agentNoteLimit {
		fail(413, "NOTE_TOO_LARGE", "note exceeds 8192 bytes")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, agentNoteLimit+1))
	var tooLarge *http.MaxBytesError
	if len(data) > agentNoteLimit || errors.As(err, &tooLarge) {
		fail(413, "NOTE_TOO_LARGE", "note exceeds 8192 bytes")
		return
	}
	if err != nil {
		fail(400, "INVALID_BODY", "could not read request body")
		return
	}
	if !utf8.Valid(data) {
		fail(400, "INVALID_UTF8", "note must be valid UTF-8")
		return
	}
	messageID := uuid.NewString()
	encoded, _ := json.Marshal(agentInstruction{MessageID: messageID, Message: string(data)})
	updated, err := replaceBridgeFile(agentNotePath, append(encoded, '\n'))
	if err != nil {
		fail(500, "NOTE_WRITE_FAILED", "note could not be saved")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "messageId": messageID, "bytes": len(data), "updatedAt": updated.UTC().Format(time.RFC3339Nano)})
}

func replaceBridgeFile(path string, data []byte) (time.Time, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return time.Time{}, err
	}
	// Refuse a redirected diagnostics directory.
	info, err := os.Lstat(dir)
	if err != nil {
		return time.Time{}, err
	}
	if !info.IsDir() {
		return time.Time{}, errors.New("invalid note directory")
	}
	f, err := os.CreateTemp(dir, ".agent-note-*") // same filesystem; mode 0600
	if err != nil {
		return time.Time{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return time.Time{}, err
	}
	if err = f.Sync(); err != nil {
		return time.Time{}, err
	}
	info, err = f.Stat()
	if err != nil {
		return time.Time{}, err
	}
	if err = f.Close(); err != nil {
		return time.Time{}, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// A reply only completes the current instruction when its ID matches. A stale
// reply is never returned, even if a manual writer finishes after a new PUT.
func (s *Server) handleAgentResponse(w http.ResponseWriter, r *http.Request) {
	agentBridgeMu.Lock()
	defer agentBridgeMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int, code, message string) { writeJSON(w, status, model.ErrorResponse(code, message)) }
	if r.URL.RawQuery != "" {
		fail(400, "INVALID_REQUEST", "query parameters are not accepted")
		return
	}
	note, err := readInstruction()
	if errors.Is(err, os.ErrNotExist) {
		fail(404, "NOTE_NOT_FOUND", "no agent note has been saved")
		return
	}
	if err != nil {
		fail(503, "NOTE_UNAVAILABLE", "saved note could not be read")
		return
	}
	result := map[string]any{"messageId": note.MessageID, "status": "pending", "response": ""}
	// Bound encoded JSON too, allowing the worst-case JSON escaping overhead.
	raw, err := readEvidenceFile(agentResponsePath, 6*agentResponseLimit+4096)
	if !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			fail(503, "RESPONSE_UNAVAILABLE", "response file is unreadable or exceeds its encoded size limit")
			return
		}
		var reply agentReply
		if !utf8.Valid(raw) || json.Unmarshal(raw, &reply) != nil || reply.MessageID == "" {
			fail(503, "INVALID_RESPONSE", "response file must contain valid UTF-8 JSON with messageId and response")
			return
		}
		// Require the response field even when the complete answer is empty.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if value, ok := fields["response"]; !ok || string(value) == "null" {
			fail(503, "INVALID_RESPONSE", "response text is required")
			return
		}
		if len(reply.Response) > agentResponseLimit {
			fail(503, "RESPONSE_TOO_LARGE", "response exceeds 65536 UTF-8 bytes")
			return
		}
		if reply.MessageID == note.MessageID {
			result["status"] = "completed"
			result["response"] = reply.Response
		}
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		return
	}
	writeJSON(w, 200, result)
}
