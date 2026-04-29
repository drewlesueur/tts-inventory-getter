package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

func APIKeyMiddleware(serviceKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Service-Key") != serviceKey {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid service key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func HMACMiddleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ts := r.Header.Get("X-Request-Timestamp")
			sig := r.Header.Get("X-Signature")
			if ts == "" || sig == "" {
				writeError(w, http.StatusUnauthorized, "HMAC_REQUIRED", "missing hmac headers")
				return
			}
			unix, err := strconv.ParseInt(ts, 10, 64)
			if err != nil || time.Since(time.Unix(unix, 0)) > 5*time.Minute {
				writeError(w, http.StatusUnauthorized, "HMAC_EXPIRED", "invalid timestamp")
				return
			}
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			h := hmac.New(sha256.New, []byte(secret))
			h.Write([]byte(ts))
			h.Write(body)
			expected := hex.EncodeToString(h.Sum(nil))
			if !hmac.Equal([]byte(expected), []byte(sig)) {
				writeError(w, http.StatusUnauthorized, "HMAC_INVALID", "signature mismatch")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "error",
		"error":  map[string]string{"code": code, "message": message},
	})
}
