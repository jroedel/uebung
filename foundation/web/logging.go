// Package web holds HTTP middleware shared by more than one App package.
//
// It lives in foundation rather than beside either app because the layering
// forbids one App package importing another, and duplicating a request log in
// each of them would let the two drift into different shapes.
package web

import (
	"log/slog"
	"net/http"
	"time"
)

// Logging wraps h to emit one line per request. A nil logger disables it, which
// is what tests want.
//
// It is applied once, outside the whole route tree, so every request is logged
// whatever serves it — including auth failures and 404s from the file server.
//
// The query string is deliberately never logged. Sign-in tokens travel there, and
// logs outlive the tokens written into them.
func Logging(log *slog.Logger, now func() time.Time, h http.Handler) http.Handler {
	if log == nil {
		return h
	}

	if now == nil {
		now = time.Now
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		h.ServeHTTP(rec, r)

		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.written,
			"duration", now().Sub(started),
		)
	})
}

// statusRecorder remembers what a handler wrote so it can be logged. WriteHeader
// may never be called, which is why status starts at 200.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.written += n

	return n, err
}
