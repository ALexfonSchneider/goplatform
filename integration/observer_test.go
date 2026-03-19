//go:build integration

package integration

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ALexfonSchneider/goplatform/pkg/observe"
)

// TestObserver_Logger verifies that obs.Logger() returns a usable logger and
// that WithBaseHandler routes logs to the provided handler.
func TestObserver_Logger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	obs, err := observe.New(
		observe.WithServiceName("logger-integ-test"),
		observe.WithBaseHandler(baseHandler),
	)
	require.NoError(t, err)

	logger := obs.Logger()
	require.NotNil(t, logger)

	// Log a message before Start.
	logger.Info("before start", "key", "value")
	assert.Contains(t, buf.String(), "before start", "log should appear in base handler")

	// Start and log again.
	ctx := context.Background()
	require.NoError(t, obs.Start(ctx))

	buf.Reset()
	logger.Info("after start", "phase", "running")
	assert.Contains(t, buf.String(), "after start", "log should appear after start")

	require.NoError(t, obs.Stop(ctx))
}

// TestObserver_ProvidersAvailableBeforeStart verifies that TracerProvider() and
// MeterProvider() return real (non-noop) providers immediately after New(),
// before Start() is called.
func TestObserver_ProvidersAvailableBeforeStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	traceExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	metricReader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	defer func() { _ = mp.Shutdown(context.Background()) }()

	obs, err := observe.New(
		observe.WithServiceName("provider-test"),
		observe.WithTracerProvider(tp),
		observe.WithMeterProvider(mp),
	)
	require.NoError(t, err)

	// TracerProvider and MeterProvider should be available immediately.
	gotTP := obs.TracerProvider()
	assert.NotNil(t, gotTP)

	gotMP := obs.MeterProvider()
	assert.NotNil(t, gotMP)

	// Create a span before Start() — should work without panic.
	ctx, span := gotTP.Tracer("test").Start(context.Background(), "before-start-span")
	span.End()
	_ = ctx

	// Verify span was recorded.
	spans := traceExporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "before-start-span", spans[0].Name)
}

// TestObserver_ContextLogger verifies that logger context methods work
// (InfoContext, ErrorContext, etc.).
func TestObserver_ContextLogger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	var buf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	traceExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	obs, err := observe.New(
		observe.WithServiceName("ctx-logger-test"),
		observe.WithBaseHandler(baseHandler),
		observe.WithTracerProvider(tp),
	)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, obs.Start(ctx))
	defer func() { _ = obs.Stop(ctx) }()

	logger := obs.Logger()

	// Create a span and log within it.
	spanCtx, span := tp.Tracer("test").Start(ctx, "test-span")
	logger.InfoContext(spanCtx, "context log message", "foo", "bar")
	span.End()

	logOutput := buf.String()
	assert.Contains(t, logOutput, "context log message")
	assert.Contains(t, logOutput, "foo")
}
