//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ALexfonSchneider/goplatform/pkg/observe"
)

// TestTrace_EndToEnd creates an Observer with an in-memory exporter (no OTLP
// endpoint needed), creates a Server with HTTPMiddleware, makes an HTTP request,
// and verifies that a span was created with the correct attributes.
// This test does not require docker-compose -- it uses in-memory exporters.
func TestTrace_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()

	// Set up in-memory trace exporter and SDK tracer provider.
	traceExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { require.NoError(t, tp.Shutdown(ctx)) }()

	// Set up in-memory metric provider with a manual reader.
	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	defer func() { require.NoError(t, mp.Shutdown(ctx)) }()

	// Create Observer with custom providers (no OTLP needed).
	obs, err := observe.New(
		observe.WithServiceName("trace-integ-test"),
		observe.WithTracerProvider(tp),
		observe.WithMeterProvider(mp),
	)
	require.NoError(t, err)
	require.NoError(t, obs.Start(ctx))
	defer func() { require.NoError(t, obs.Stop(ctx)) }()

	// Create an HTTP handler wrapped with the observe middleware.
	innerHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	middleware := observe.HTTPMiddleware(obs.TracerProvider(), obs.MeterProvider())
	handler := middleware(innerHandler)

	// Make an HTTP request via httptest.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Verify the response.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"status":"ok"}`, rec.Body.String())

	// Verify a span was created.
	spans := traceExporter.GetSpans()
	require.Len(t, spans, 1, "exactly one span should have been created")
	assert.Equal(t, "HTTP GET /api/v1/health", spans[0].Name)

	// Verify span attributes.
	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}
	assert.Equal(t, "GET", attrMap["http.method"])
	assert.Equal(t, int64(200), attrMap["http.status_code"])
}
