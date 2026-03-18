package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// responseWriter wraps http.ResponseWriter to capture the status code written
// by the handler. This allows middleware to inspect the status after the handler
// has completed.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code and delegates to the wrapped ResponseWriter.
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write delegates to the wrapped ResponseWriter and records an implicit 200
// status if WriteHeader was not called.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying http.ResponseWriter so that callers can access
// optional interfaces such as http.Flusher.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Recovery returns middleware that recovers from panics in HTTP handlers.
// It logs the panic stack trace and returns a 500 Internal Server Error response.
// The server continues running after a panic.
func Recovery(log platform.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)
					log.Error("server: panic recovered",
						"panic", fmt.Sprintf("%v", rec),
						"stack", string(buf[:n]),
						"method", r.Method,
						"path", r.URL.Path,
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"error": "internal server error",
					})
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogging returns middleware that logs HTTP requests with method, path,
// status code, duration, and request_id. Paths listed in skipPaths are not
// logged, which is useful for high-frequency endpoints such as /healthz/live
// and /metrics.
func RequestLogging(log platform.Logger, skipPaths ...string) func(http.Handler) http.Handler {
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = generateRequestID()
			}

			rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rw, r)

			duration := time.Since(start)
			log.Info("server: request completed",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration", duration.String(),
				"request_id", requestID,
			)
		})
	}
}

// generateRequestID produces a random UUID-like identifier using crypto/rand.
func generateRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
