//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ALexfonSchneider/goplatform/pkg/observe"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// TestE2E_RequestFlow performs a full end-to-end flow:
//  1. Starts an App with Server + Observer (in-memory exporter).
//  2. Registers a REST endpoint that returns 200 with a JSON body.
//  3. Makes an HTTP request to the endpoint.
//  4. Verifies: the response is correct and a span was created.
//  5. Shuts down the App gracefully via SIGTERM.
func TestE2E_RequestFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()

	// Set up in-memory trace exporter.
	traceExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))

	// Set up in-memory metric provider.
	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))

	// Create Observer with in-memory providers.
	obs, err := observe.New(
		observe.WithServiceName("e2e-integ-test"),
		observe.WithTracerProvider(tp),
		observe.WithMeterProvider(mp),
	)
	require.NoError(t, err)

	// Create Server on a random port with the tracing middleware.
	srv, err := server.New(
		server.WithAddr(":0"),
	)
	require.NoError(t, err)

	// Apply the observe HTTP middleware to the server.
	srv.Use(observe.HTTPMiddleware(tp, mp))

	// Register a REST endpoint.
	srv.Route("/api/v1", func(r chi.Router) {
		r.Get("/status", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"service": "e2e-integ-test",
				"status":  "healthy",
			})
		})
	})

	// Build App with Observer and Server.
	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	require.NoError(t, app.Register("observer", obs))
	require.NoError(t, app.Register("server", srv))

	// Start App in a goroutine.
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(ctx)
	}()

	// Wait for the server to be ready.
	time.Sleep(500 * time.Millisecond)

	addr := srv.Addr()
	require.NotEmpty(t, addr, "server should be listening")
	baseURL := fmt.Sprintf("http://%s", addr)

	// Make an HTTP request to the REST endpoint.
	resp, err := http.Get(baseURL + "/api/v1/status")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify the response.
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "e2e-integ-test", body["service"])
	assert.Equal(t, "healthy", body["status"])

	// Verify a span was created for the request.
	spans := traceExporter.GetSpans()
	require.NotEmpty(t, spans, "at least one span should have been created")

	// Find the span for our endpoint.
	var found bool
	for _, s := range spans {
		if s.Name == "HTTP GET /api/v1/status" {
			found = true
			break
		}
	}
	assert.True(t, found, "should find a span for HTTP GET /api/v1/status")

	// Send SIGTERM to shut down the App gracefully.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	// Wait for Run to return.
	select {
	case err := <-runErr:
		assert.NoError(t, err, "App.Run should return nil on graceful shutdown")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for App.Run to return")
	}

	// Clean up OTel providers.
	require.NoError(t, tp.Shutdown(ctx))
	require.NoError(t, mp.Shutdown(ctx))
}
