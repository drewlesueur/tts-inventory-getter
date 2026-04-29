package api

import (
	"encoding/json"
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

func bodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func rateLimit(rps, burst int) func(http.Handler) http.Handler {
	var (
		mu      sync.Mutex
		buckets = map[string]*rate.Limiter{}
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			mu.Lock()
			lim, ok := buckets[ip]
			if !ok {
				lim = rate.NewLimiter(rate.Limit(rps), burst)
				buckets[ip] = lim
			}
			mu.Unlock()
			if !lim.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "error": map[string]string{"code": "RATE_LIMITED", "message": "too many requests"}})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func chain(h http.Handler, m ...func(http.Handler) http.Handler) http.Handler {
	for i := len(m) - 1; i >= 0; i-- {
		h = m[i](h)
	}
	return h
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if host == "" {
		return r.RemoteAddr
	}
	return host
}
