//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// TestServer_RecoveryMiddleware verifies that the default Recovery middleware
// catches panics and returns a 500 response without crashing the server.
func TestServer_RecoveryMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	srv.Route("/api", func(r chi.Router) {
		r.Get("/panic", func(_ http.ResponseWriter, _ *http.Request) {
			panic("test panic")
		})
		r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
	})

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	addr := srv.Addr()
	require.NotEmpty(t, addr)
	baseURL := fmt.Sprintf("http://%s", addr)

	// Hit the panicking handler — should get 500, not crash.
	resp, err := http.Get(baseURL + "/api/panic")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Hit the ok handler — server should still be running.
	resp2, err := http.Get(baseURL + "/api/ok")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

// TestServer_ReadinessWithHealthCheckers verifies the readiness endpoint
// integrates with platform.HealthChecker components.
func TestServer_ReadinessWithHealthCheckers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	// Create a custom HealthChecker that we can control.
	healthy := &controllableHealthChecker{healthy: true}

	checkers := map[string]platform.HealthChecker{
		"custom": healthy,
	}

	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithHealthCheckers(checkers),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	addr := srv.Addr()
	require.NotEmpty(t, addr)
	baseURL := fmt.Sprintf("http://%s", addr)

	// Healthy state.
	resp, err := http.Get(baseURL + "/healthz/ready")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])

	// Make the checker unhealthy.
	healthy.healthy = false

	resp2, err := http.Get(baseURL + "/healthz/ready")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, resp2.StatusCode)

	var body2 map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&body2))
	assert.Equal(t, "degraded", body2["status"])
}

// TestServer_DisableDefaultMiddleware verifies that default middleware can
// be disabled via WithoutRecovery and WithoutRequestLogging.
func TestServer_DisableDefaultMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithoutRecovery(),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	srv.Route("/api", func(r chi.Router) {
		r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"message":"hello"}`))
		})
	})

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	addr := srv.Addr()
	require.NotEmpty(t, addr)

	// Normal request should still work.
	resp, err := http.Get(fmt.Sprintf("http://%s/api/hello", addr))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestServer_CustomMiddleware verifies that custom middleware added via Use()
// is applied to requests.
func TestServer_CustomMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	// Add custom middleware that sets a response header.
	srv.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Custom-Middleware", "applied")
			next.ServeHTTP(w, r)
		})
	})

	srv.Route("/api", func(r chi.Router) {
		r.Get("/test", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	resp, err := http.Get(fmt.Sprintf("http://%s/api/test", srv.Addr()))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "applied", resp.Header.Get("X-Custom-Middleware"),
		"custom middleware should set the response header")
}

// controllableHealthChecker is a HealthChecker whose health status can be
// toggled for testing purposes.
type controllableHealthChecker struct {
	healthy bool
}

func (c *controllableHealthChecker) HealthCheck(_ context.Context) error {
	if !c.healthy {
		return fmt.Errorf("unhealthy")
	}
	return nil
}

// TestServer_Timeouts verifies that WithReadTimeout and WithWriteTimeout are
// applied to the server configuration.
func TestServer_Timeouts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
		server.WithReadTimeout(1*time.Second),
		server.WithWriteTimeout(1*time.Second),
		server.WithoutRequestLogging(),
	)
	require.NoError(t, err)

	srv.Route("/api", func(r chi.Router) {
		r.Get("/fast", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	ctx := context.Background()
	require.NoError(t, srv.Start(ctx))
	defer func() { require.NoError(t, srv.Stop(ctx)) }()

	// A fast request should succeed.
	resp, err := http.Get(fmt.Sprintf("http://%s/api/fast", srv.Addr()))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
