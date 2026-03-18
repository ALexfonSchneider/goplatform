package observe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestObserver_TwoInstances(t *testing.T) {
	exp1 := tracetest.NewInMemoryExporter()
	tp1 := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp1))

	exp2 := tracetest.NewInMemoryExporter()
	tp2 := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp2))

	obs1, err := New(
		WithServiceName("svc-1"),
		WithTracerProvider(tp1),
	)
	require.NoError(t, err)

	obs2, err := New(
		WithServiceName("svc-2"),
		WithTracerProvider(tp2),
	)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, obs1.Start(ctx))
	require.NoError(t, obs2.Start(ctx))

	// Create spans on each observer — they should be independent.
	_, span1 := obs1.Tracer("test").Start(ctx, "span-from-obs1")
	span1.End()

	_, span2 := obs2.Tracer("test").Start(ctx, "span-from-obs2")
	span2.End()

	spans1 := exp1.GetSpans()
	spans2 := exp2.GetSpans()

	require.Len(t, spans1, 1)
	require.Len(t, spans2, 1)

	assert.Equal(t, "span-from-obs1", spans1[0].Name)
	assert.Equal(t, "span-from-obs2", spans2[0].Name)

	require.NoError(t, obs1.Stop(ctx))
	require.NoError(t, obs2.Stop(ctx))

	// Clean up SDK tracer providers we created for the test.
	require.NoError(t, tp1.Shutdown(ctx))
	require.NoError(t, tp2.Shutdown(ctx))
}

func TestHTTPMiddleware(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	handler := HTTPMiddleware(tp, mp)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "HTTP GET /test-path", spans[0].Name)

	// Verify span attributes.
	attrMap := make(map[string]any)
	for _, attr := range spans[0].Attributes {
		attrMap[string(attr.Key)] = attr.Value.AsInterface()
	}
	assert.Equal(t, "GET", attrMap["http.method"])
	assert.Equal(t, int64(200), attrMap["http.status_code"])

	// Verify metrics were recorded.
	var rm metricdata.ResourceMetrics
	err := reader.Collect(context.Background(), &rm)
	require.NoError(t, err)
	assert.NotEmpty(t, rm.ScopeMetrics, "should have recorded metrics")

	require.NoError(t, tp.Shutdown(context.Background()))
	require.NoError(t, mp.Shutdown(context.Background()))
}

func TestConnectInterceptor(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	interceptor := ConnectInterceptor(tp)

	// Create a minimal unary func that the interceptor wraps.
	called := false
	inner := connect.UnaryFunc(func(_ context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return &connect.Response[any]{}, nil
	})

	wrapped := interceptor(inner)

	// Build a request. Spec fields are not settable on a plain NewRequest,
	// so the procedure will default to "". We verify the interceptor runs
	// without panicking and creates a span.
	fakeReq := connect.NewRequest[any](nil)

	_, err := wrapped(context.Background(), fakeReq)
	require.NoError(t, err)
	assert.True(t, called, "inner handler should have been called")

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	// Default procedure on a plain request is "".
	assert.Equal(t, "", spans[0].Name)

	require.NoError(t, tp.Shutdown(context.Background()))
}

func TestObserver_TracerProviderBeforeStart(t *testing.T) {
	obs, err := New(WithServiceName("eager-svc"))
	require.NoError(t, err)

	// TracerProvider should return a real SDK provider BEFORE Start().
	tp := obs.TracerProvider()
	require.NotNil(t, tp)

	// Create a span before Start() — should not panic, should be recorded.
	ctx, span := tp.Tracer("test").Start(context.Background(), "before-start-span")
	span.End()

	// The span is recorded by the SDK provider (not noop).
	// Verify by checking that the context carries a valid span.
	recordedSpan := trace.SpanFromContext(ctx)
	assert.True(t, recordedSpan.SpanContext().IsValid(), "span created before Start() should be valid")
	assert.True(t, recordedSpan.SpanContext().HasTraceID(), "span should have a trace ID")
}

func TestObserver_SpanExportedAfterStart(t *testing.T) {
	// Create Observer without OTLP (no real endpoint).
	// Inject a sync exporter to capture spans.
	exp := tracetest.NewInMemoryExporter()

	obs, err := New(WithServiceName("export-svc"))
	require.NoError(t, err)

	// Register the sync exporter on the owned TP.
	// This simulates what Start() does with OTLP, but synchronously.
	obs.ownedTP.RegisterSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp))

	// Create a span — it should be exported via the registered processor.
	_, span := obs.TracerProvider().Tracer("test").Start(context.Background(), "exported-span")
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "exported-span", spans[0].Name)

	require.NoError(t, obs.Stop(context.Background()))
}

func TestObserver_MeterProviderBeforeStart(t *testing.T) {
	obs, err := New(WithServiceName("meter-svc"))
	require.NoError(t, err)

	// MeterProvider should return a real SDK provider BEFORE Start().
	mp := obs.MeterProvider()
	require.NotNil(t, mp)

	// Create a counter before Start() — should not panic.
	counter, err := mp.Meter("test").Int64Counter("test_counter")
	require.NoError(t, err)
	counter.Add(context.Background(), 1) // no panic
}

func TestObserver_StartStop(t *testing.T) {
	obs, err := New(
		WithServiceName("test-svc"),
		WithServiceVersion("1.0.0"),
	)
	require.NoError(t, err)

	ctx := context.Background()

	require.NoError(t, obs.Start(ctx))

	// Verify providers are available.
	assert.NotNil(t, obs.TracerProvider())
	assert.NotNil(t, obs.MeterProvider())
	assert.NotNil(t, obs.Logger())
	assert.NotNil(t, obs.Tracer("test"))
	assert.NotNil(t, obs.Meter("test"))

	require.NoError(t, obs.Stop(ctx))
}
