package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"time"
)

// apiKeyAuth rejects requests missing a valid X-API-Key header.
// /healthz is exempt so orchestrators can probe liveness without a
// key. Uses a constant-time comparison to avoid leaking key length/
// prefix via response-timing side channels.
func apiKeyAuth(expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-API-Key")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid X-API-Key header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code written by the wrapped
// handler so requestLogger can log it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// requestLogger logs method/path/status/duration only — never request
// or response bodies (which may contain credentials or email content).
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}
