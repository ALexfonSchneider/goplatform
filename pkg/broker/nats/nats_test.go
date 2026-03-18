package nats

import (
	"context"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

func TestPublisher_Options(t *testing.T) {
	t.Run("default URL is nats default", func(t *testing.T) {
		p, err := NewPublisher()
		require.NoError(t, err)
		assert.Equal(t, natsgo.DefaultURL, p.url)
		assert.False(t, p.jetStream)
		assert.NotNil(t, p.logger)
		assert.Empty(t, p.hooks)
	})

	t.Run("custom URL", func(t *testing.T) {
		p, err := NewPublisher(WithURL("nats://custom:4222"))
		require.NoError(t, err)
		assert.Equal(t, "nats://custom:4222", p.url)
	})

	t.Run("with jetstream", func(t *testing.T) {
		p, err := NewPublisher(WithJetStream())
		require.NoError(t, err)
		assert.True(t, p.jetStream)
	})

	t.Run("with publish hooks", func(t *testing.T) {
		hook := func(_ context.Context, _ string, _ string, payload []byte) ([]byte, error) {
			return payload, nil
		}
		p, err := NewPublisher(WithPublishHook(hook))
		require.NoError(t, err)
		assert.Len(t, p.hooks, 1)
	})

	t.Run("custom logger", func(t *testing.T) {
		logger := platform.NopLogger()
		p, err := NewPublisher(WithPublisherLogger(logger))
		require.NoError(t, err)
		assert.NotNil(t, p.logger)
	})

	t.Run("with tracer provider", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		defer func() { _ = tp.Shutdown(context.Background()) }()

		p, err := NewPublisher(WithPublisherTracerProvider(tp))
		require.NoError(t, err)
		assert.Equal(t, tp, p.tracerProvider)
	})

	t.Run("with nats options", func(t *testing.T) {
		p, err := NewPublisher(WithNATSOptions(natsgo.Name("test-pub")))
		require.NoError(t, err)
		assert.Len(t, p.natsOpts, 1)
	})
}

func TestPublisher_Defaults(t *testing.T) {
	p, err := NewPublisher()
	require.NoError(t, err)

	assert.Equal(t, natsgo.DefaultURL, p.url)
	assert.False(t, p.jetStream)
	assert.NotNil(t, p.logger)
	assert.NotNil(t, p.tracerProvider)
	assert.NotNil(t, p.tracer)
	assert.Empty(t, p.hooks)
	assert.Empty(t, p.natsOpts)
	assert.Nil(t, p.conn)
	assert.Nil(t, p.js)
}

func TestSubscriber_Options(t *testing.T) {
	t.Run("default URL is nats default", func(t *testing.T) {
		s, err := NewSubscriber()
		require.NoError(t, err)
		assert.Equal(t, natsgo.DefaultURL, s.url)
		assert.False(t, s.jetStream)
		assert.NotNil(t, s.logger)
		assert.Empty(t, s.middlewares)
		assert.Empty(t, s.queueGroup)
		assert.Empty(t, s.durable)
		assert.Zero(t, s.maxRetries)
	})

	t.Run("custom URL", func(t *testing.T) {
		s, err := NewSubscriber(WithSubscriberURL("nats://custom:4222"))
		require.NoError(t, err)
		assert.Equal(t, "nats://custom:4222", s.url)
	})

	t.Run("with jetstream", func(t *testing.T) {
		s, err := NewSubscriber(WithSubscriberJetStream())
		require.NoError(t, err)
		assert.True(t, s.jetStream)
	})

	t.Run("with durable", func(t *testing.T) {
		s, err := NewSubscriber(WithDurable("my-durable"))
		require.NoError(t, err)
		assert.Equal(t, "my-durable", s.durable)
	})

	t.Run("with queue group", func(t *testing.T) {
		s, err := NewSubscriber(WithQueueGroup("my-group"))
		require.NoError(t, err)
		assert.Equal(t, "my-group", s.queueGroup)
	})

	t.Run("with middleware", func(t *testing.T) {
		mw := func(next broker.Handler) broker.Handler {
			return next
		}
		s, err := NewSubscriber(WithMiddleware(mw))
		require.NoError(t, err)
		assert.Len(t, s.middlewares, 1)
	})

	t.Run("with dlq", func(t *testing.T) {
		pub, err := NewPublisher()
		require.NoError(t, err)

		s, sErr := NewSubscriber(WithDLQ(pub, ".dlq"))
		require.NoError(t, sErr)
		assert.Equal(t, pub, s.dlqPublisher)
		assert.Equal(t, ".dlq", s.dlqTopicSuffix)
	})

	t.Run("with retry", func(t *testing.T) {
		s, err := NewSubscriber(WithRetry(3, 100*time.Millisecond))
		require.NoError(t, err)
		assert.Equal(t, 3, s.maxRetries)
		assert.Equal(t, 100*time.Millisecond, s.retryBackoff)
	})

	t.Run("with subscriber logger", func(t *testing.T) {
		logger := platform.NopLogger()
		s, err := NewSubscriber(WithSubscriberLogger(logger))
		require.NoError(t, err)
		assert.NotNil(t, s.logger)
	})

	t.Run("with subscriber tracer provider", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		defer func() { _ = tp.Shutdown(context.Background()) }()

		s, err := NewSubscriber(WithSubscriberTracerProvider(tp))
		require.NoError(t, err)
		assert.Equal(t, tp, s.tracerProvider)
	})

	t.Run("with nats options", func(t *testing.T) {
		s, err := NewSubscriber(WithSubscriberNATSOptions(natsgo.Name("test-sub")))
		require.NoError(t, err)
		assert.Len(t, s.natsOpts, 1)
	})
}

func TestSubscriber_Defaults(t *testing.T) {
	s, err := NewSubscriber()
	require.NoError(t, err)

	assert.Equal(t, natsgo.DefaultURL, s.url)
	assert.False(t, s.jetStream)
	assert.NotNil(t, s.logger)
	assert.NotNil(t, s.tracerProvider)
	assert.NotNil(t, s.tracer)
	assert.Empty(t, s.middlewares)
	assert.Empty(t, s.queueGroup)
	assert.Empty(t, s.durable)
	assert.Zero(t, s.maxRetries)
	assert.Zero(t, s.retryBackoff)
	assert.Nil(t, s.dlqPublisher)
	assert.Empty(t, s.dlqTopicSuffix)
	assert.Empty(t, s.natsOpts)
	assert.Nil(t, s.conn)
	assert.Nil(t, s.js)
}

func TestNATSHeaderCarrier(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		carrier.Set("traceparent", "00-abc123-def456-01")
		assert.Equal(t, "00-abc123-def456-01", carrier.Get("traceparent"))
	})

	t.Run("get missing key returns empty", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		assert.Equal(t, "", carrier.Get("nonexistent"))
	})

	t.Run("set overwrites existing", func(t *testing.T) {
		h := make(natsgo.Header)
		h.Set("key1", "value1")
		carrier := natsHeaderCarrier(h)
		carrier.Set("key1", "value2")
		assert.Equal(t, "value2", carrier.Get("key1"))
	})

	t.Run("keys returns all keys", func(t *testing.T) {
		h := make(natsgo.Header)
		h.Set("traceparent", "v1")
		h.Set("tracestate", "v2")
		carrier := natsHeaderCarrier(h)
		keys := carrier.Keys()
		assert.ElementsMatch(t, []string{"traceparent", "tracestate"}, keys)
	})

	t.Run("keys on empty carrier", func(t *testing.T) {
		carrier := natsHeaderCarrier(make(natsgo.Header))
		keys := carrier.Keys()
		assert.Empty(t, keys)
	})
}

func TestInjectExtractTraceContext(t *testing.T) {
	// Create a trace provider and span to have a valid trace context.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	spanCtx := trace.SpanFromContext(ctx).SpanContext()
	require.True(t, spanCtx.IsValid(), "span context must be valid")

	// Inject trace context into NATS headers.
	header := InjectTraceContext(ctx, nil)
	assert.NotEmpty(t, header, "header should contain trace context")

	// Verify traceparent header was injected.
	assert.NotEmpty(t, header.Get("traceparent"), "traceparent header must be present")

	// Extract trace context from headers into a fresh context.
	extractedCtx := ExtractTraceContext(context.Background(), header)
	extractedSpanCtx := trace.SpanFromContext(extractedCtx).SpanContext()

	assert.Equal(t, spanCtx.TraceID(), extractedSpanCtx.TraceID(),
		"extracted trace ID must match original")
	assert.Equal(t, spanCtx.SpanID(), extractedSpanCtx.SpanID(),
		"extracted span ID must match original")
}

func TestInjectTraceContext_AppendsToExistingHeaders(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	existing := make(natsgo.Header)
	existing.Set("custom-header", "custom-value")

	header := InjectTraceContext(ctx, existing)
	assert.True(t, len(header) > 1, "should have both custom and trace headers")

	// Verify custom header is preserved.
	assert.Equal(t, "custom-value", header.Get("custom-header"), "custom header must be preserved")
}

func TestInjectTraceContext_NilHeader(t *testing.T) {
	header := InjectTraceContext(context.Background(), nil)
	assert.NotNil(t, header, "should create new header when nil is passed")
}

func TestExtractTraceContext_NilHeader(t *testing.T) {
	ctx := context.Background()
	result := ExtractTraceContext(ctx, nil)
	assert.NotNil(t, result, "should return valid context when nil header is passed")
}

func TestNATSHeadersToMap(t *testing.T) {
	t.Run("converts headers to map", func(t *testing.T) {
		h := make(natsgo.Header)
		h.Set("key1", "value1")
		h.Set("key2", "value2")
		m := natsHeadersToMap(h)
		assert.Equal(t, "value1", m["key1"])
		assert.Equal(t, "value2", m["key2"])
	})

	t.Run("nil for empty headers", func(t *testing.T) {
		m := natsHeadersToMap(nil)
		assert.Nil(t, m)
	})

	t.Run("nil for zero-length headers", func(t *testing.T) {
		m := natsHeadersToMap(make(natsgo.Header))
		assert.Nil(t, m)
	})
}

func TestPublisher_PublishBeforeStart(t *testing.T) {
	p, err := NewPublisher()
	require.NoError(t, err)

	err = p.Publish(context.Background(), "topic", "key", []byte("value"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestSubscriber_SubscribeBeforeStart(t *testing.T) {
	s, err := NewSubscriber()
	require.NoError(t, err)

	err = s.Subscribe(context.Background(), "topic", func(_ context.Context, _ broker.Message) error {
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestPublisher_StopBeforeStart(t *testing.T) {
	p, err := NewPublisher()
	require.NoError(t, err)

	err = p.Stop(context.Background())
	require.NoError(t, err)
}

func TestSubscriber_StopBeforeStart(t *testing.T) {
	s, err := NewSubscriber()
	require.NoError(t, err)

	err = s.Stop(context.Background())
	require.NoError(t, err)
}

func TestApplyMiddleware(t *testing.T) {
	t.Run("no middleware returns original handler", func(t *testing.T) {
		called := false
		h := func(_ context.Context, _ broker.Message) error {
			called = true
			return nil
		}
		wrapped := applyMiddleware(h, nil)
		err := wrapped(context.Background(), broker.Message{})
		require.NoError(t, err)
		assert.True(t, called)
	})

	t.Run("middleware applied in correct order", func(t *testing.T) {
		var order []int

		mw1 := func(next broker.Handler) broker.Handler {
			return func(ctx context.Context, msg broker.Message) error {
				order = append(order, 1)
				return next(ctx, msg)
			}
		}
		mw2 := func(next broker.Handler) broker.Handler {
			return func(ctx context.Context, msg broker.Message) error {
				order = append(order, 2)
				return next(ctx, msg)
			}
		}

		h := func(_ context.Context, _ broker.Message) error {
			order = append(order, 3)
			return nil
		}

		wrapped := applyMiddleware(h, []broker.Middleware{mw1, mw2})
		err := wrapped(context.Background(), broker.Message{})
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, order)
	})
}
