//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/redis"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// TestIdempotency_WithRedis tests the IdempotencyMiddleware using a real Redis
// instance as the backing store. It sends the same request twice with the same
// Idempotency-Key and verifies the second request returns the cached response
// without executing the handler again.
func TestIdempotency_WithRedis(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start Redis client.
	redisClient, err := redis.New(
		redis.WithAddr(redisAddr),
	)
	require.NoError(t, err)
	require.NoError(t, redisClient.Start(ctx))
	defer func() { require.NoError(t, redisClient.Stop(ctx)) }()

	// Track how many times the handler is called.
	handlerCallCount := 0

	// Create server with idempotency middleware.
	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	// Apply idempotency middleware.
	srv.Use(server.IdempotencyMiddleware(redisClient, 30*time.Second))

	// Register a handler.
	srv.Route("/api", func(r chi.Router) {
		r.Post("/create", func(w http.ResponseWriter, _ *http.Request) {
			handlerCallCount++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"result": "created",
			})
		})
	})

	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	addr := srv.Addr()
	require.NotEmpty(t, addr)
	baseURL := "http://" + addr

	idemKey := "integ-idem-" + t.Name()

	// First request — should execute the handler.
	req1, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/create",
		strings.NewReader(`{"data":"test"}`))
	require.NoError(t, err)
	req1.Header.Set("Idempotency-Key", idemKey)
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := http.DefaultClient.Do(req1)
	require.NoError(t, err)
	defer func() { _ = resp1.Body.Close() }()

	assert.Equal(t, http.StatusCreated, resp1.StatusCode)
	var body1 map[string]string
	require.NoError(t, json.NewDecoder(resp1.Body).Decode(&body1))
	assert.Equal(t, "created", body1["result"])
	assert.Equal(t, 1, handlerCallCount, "handler should be called once")

	// Second request with the same key — should return cached response.
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/create",
		strings.NewReader(`{"data":"test"}`))
	require.NoError(t, err)
	req2.Header.Set("Idempotency-Key", idemKey)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()

	assert.Equal(t, http.StatusCreated, resp2.StatusCode, "cached response should have same status")
	var body2 map[string]string
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&body2))
	assert.Equal(t, "created", body2["result"], "cached response should have same body")
	assert.Equal(t, 1, handlerCallCount, "handler should NOT be called again")

	// Third request WITHOUT idempotency key — should always execute handler.
	req3, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/create",
		strings.NewReader(`{"data":"test"}`))
	require.NoError(t, err)
	req3.Header.Set("Content-Type", "application/json")

	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer func() { _ = resp3.Body.Close() }()

	assert.Equal(t, http.StatusCreated, resp3.StatusCode)
	assert.Equal(t, 2, handlerCallCount, "handler should be called without idempotency key")

	// Clean up the Redis key.
	_ = redisClient.Del(ctx, "idem:"+idemKey)
}
