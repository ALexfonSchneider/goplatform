package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"
)

// IdempotencyStore is the interface for storing idempotency keys and responses.
// Implementations must be safe for concurrent use.
type IdempotencyStore interface {
	// SetNX sets the value if the key does not already exist. It returns true
	// if the value was set (key was new) and false if the key already existed.
	SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error)

	// Get retrieves the stored value for the key and unmarshals it into dest.
	Get(ctx context.Context, key string, dest any) error

	// Set stores the value for the key with the given TTL, overwriting any
	// existing value.
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
}

// cachedResponse holds the HTTP response data stored by the idempotency
// middleware so that duplicate requests receive the same response.
type cachedResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

// IdempotencyMiddleware returns middleware that deduplicates requests based on
// the Idempotency-Key header.
//
// Flow:
//  1. No Idempotency-Key header → pass through to handler (no dedup)
//  2. New key (SetNX returns true) → execute handler, cache response, return result
//  3. Existing key, still processing → 409 Conflict (concurrent duplicate)
//  4. Existing key, completed → return cached response (same status, headers, body)
//
// The store's TTL controls how long responses are cached. After TTL expires,
// the same key can be reused for a new request.
//
// IdempotencyStore is typically backed by Redis (pkg/redis.Client implements it).
func IdempotencyMiddleware(store IdempotencyStore, ttl time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			storeKey := "idem:" + key

			isNew, err := store.SetNX(r.Context(), storeKey, "processing", ttl)
			if err != nil {
				http.Error(w, `{"error":"idempotency store error"}`, http.StatusInternalServerError)
				return
			}

			if isNew {
				// First request with this key: execute the handler and capture
				// the response.
				rec := httptest.NewRecorder()
				next.ServeHTTP(rec, r)

				result := rec.Result()
				defer func() { _ = result.Body.Close() }()

				body := rec.Body.Bytes()

				headers := make(map[string]string, len(result.Header))
				for k := range result.Header {
					headers[k] = result.Header.Get(k)
				}

				cached := cachedResponse{
					StatusCode: result.StatusCode,
					Headers:    headers,
					Body:       body,
				}

				// Store the completed response; ignore errors since the
				// response has already been computed.
				_ = store.Set(r.Context(), storeKey, cached, ttl)

				// Write the captured response to the real ResponseWriter.
				for k, v := range headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(result.StatusCode)
				_, _ = w.Write(body)
				return
			}

			// Key already exists — check whether the original request is still
			// processing or has completed.
			var raw json.RawMessage
			if err := store.Get(r.Context(), storeKey, &raw); err != nil {
				http.Error(w, `{"error":"idempotency store error"}`, http.StatusInternalServerError)
				return
			}

			// Try to decode as a completed response first. If it has the
			// expected structure, return the cached result.
			var cached cachedResponse
			if err := json.Unmarshal(raw, &cached); err == nil && cached.StatusCode != 0 {
				for k, v := range cached.Headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(cached.StatusCode)
				_, _ = w.Write(cached.Body)
				return
			}

			// Not a completed response — the original request is still
			// processing.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"concurrent request with same idempotency key"}`))
		})
	}
}
