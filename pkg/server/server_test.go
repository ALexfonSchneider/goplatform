package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// healthChecker is a test helper that implements platform.HealthChecker.
type healthChecker struct {
	err error
}

func (h *healthChecker) HealthCheck(_ context.Context) error {
	return h.err
}

// startTestServer creates and starts a server on a random port, returning the
// server and its base URL. The caller must stop the server.
func startTestServer(t *testing.T, opts ...Option) (*Server, string) {
	t.Helper()

	opts = append([]Option{WithAddr(":0")}, opts...)
	srv, err := New(opts...)
	require.NoError(t, err)

	err = srv.Start(context.Background())
	require.NoError(t, err)

	addr := srv.Addr()
	require.NotEmpty(t, addr)

	return srv, "http://" + addr
}

func TestServer_StartStop(t *testing.T) {
	srv, baseURL := startTestServer(t)

	// Verify the server responds.
	resp, err := http.Get(baseURL + "/healthz/live")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// Record the address to check the port is released.
	addr := srv.Addr()

	// Stop the server.
	err = srv.Stop(context.Background())
	require.NoError(t, err)

	// Verify the port is released by binding to the same address.
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err, "port should be released after Stop")
	require.NoError(t, ln.Close())
}

func TestServer_RESTEndpoint(t *testing.T) {
	srv, baseURL := startTestServer(t)
	defer func() {
		require.NoError(t, srv.Stop(context.Background()))
	}()

	srv.Route("/api", func(r chi.Router) {
		r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello world"))
		})
	})

	resp, err := http.Get(baseURL + "/api/hello")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(body))
}

func TestServer_GracefulShutdown(t *testing.T) {
	srv, baseURL := startTestServer(t)

	srv.Route("/slow", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("done"))
		})
	})

	// Start an in-flight request.
	resultCh := make(chan struct {
		resp *http.Response
		err  error
	}, 1)

	go func() {
		resp, err := http.Get(baseURL + "/slow/")
		resultCh <- struct {
			resp *http.Response
			err  error
		}{resp, err}
	}()

	// Give the request a moment to be received by the server.
	time.Sleep(50 * time.Millisecond)

	// Initiate graceful shutdown — should wait for in-flight request.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	err := srv.Stop(stopCtx)
	require.NoError(t, err)

	// The in-flight request should have completed successfully.
	result := <-resultCh
	require.NoError(t, result.err)
	defer func() { require.NoError(t, result.resp.Body.Close()) }()
	assert.Equal(t, http.StatusOK, result.resp.StatusCode)

	body, err := io.ReadAll(result.resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "done", string(body))

	// New request after Stop should fail.
	_, err = http.Get(baseURL + "/healthz/live")
	assert.Error(t, err, "request after Stop should fail")
}

func TestServer_HealthLive(t *testing.T) {
	srv, baseURL := startTestServer(t)
	defer func() {
		require.NoError(t, srv.Stop(context.Background()))
	}()

	resp, err := http.Get(baseURL + "/healthz/live")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body healthResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body.Status)
}

func TestServer_HealthReady_Healthy(t *testing.T) {
	checkers := map[string]platform.HealthChecker{
		"db":    &healthChecker{err: nil},
		"cache": &healthChecker{err: nil},
	}

	srv, baseURL := startTestServer(t, WithHealthCheckers(checkers))
	defer func() {
		require.NoError(t, srv.Stop(context.Background()))
	}()

	resp, err := http.Get(baseURL + "/healthz/ready")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body healthResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "ok", body.Checks["db"])
	assert.Equal(t, "ok", body.Checks["cache"])
}

func TestServer_HealthReady_Unhealthy(t *testing.T) {
	checkers := map[string]platform.HealthChecker{
		"db":    &healthChecker{err: nil},
		"cache": &healthChecker{err: errors.New("connection refused")},
	}

	srv, baseURL := startTestServer(t, WithHealthCheckers(checkers))
	defer func() {
		require.NoError(t, srv.Stop(context.Background()))
	}()

	resp, err := http.Get(baseURL + "/healthz/ready")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var body healthResponse
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "degraded", body.Status)
	assert.Equal(t, "ok", body.Checks["db"])
	assert.Equal(t, "connection refused", body.Checks["cache"])
}

func TestServer_WithoutObserver(t *testing.T) {
	// Server should work without any observer/tracing/logger setup.
	srv, baseURL := startTestServer(t)
	defer func() {
		require.NoError(t, srv.Stop(context.Background()))
	}()

	resp, err := http.Get(baseURL + "/healthz/live")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Mount a route and verify it works.
	srv.Route("/ping", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "pong")
		})
	})

	resp2, err := http.Get(baseURL + "/ping/")
	require.NoError(t, err)
	defer func() { require.NoError(t, resp2.Body.Close()) }()

	body, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(body))
}
