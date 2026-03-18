package kafka

import (
	"context"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

func TestProducer_Options(t *testing.T) {
	t.Run("default logger is nop", func(t *testing.T) {
		p, err := NewProducer(WithBrokers("localhost:9092"))
		require.NoError(t, err)
		assert.NotNil(t, p.logger)
		assert.Equal(t, []string{"localhost:9092"}, p.brokers)
	})

	t.Run("requires at least one broker", func(t *testing.T) {
		_, err := NewProducer()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one broker address")
	})

	t.Run("custom logger", func(t *testing.T) {
		logger := platform.NopLogger()
		p, err := NewProducer(
			WithBrokers("broker1:9092", "broker2:9092"),
			WithProducerLogger(logger),
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, p.brokers)
	})

	t.Run("with publish hooks", func(t *testing.T) {
		hook := func(_ context.Context, _ string, _ string, payload []byte) ([]byte, error) {
			return payload, nil
		}
		p, err := NewProducer(
			WithBrokers("localhost:9092"),
			WithPublishHook(hook),
		)
		require.NoError(t, err)
		assert.Len(t, p.hooks, 1)
	})

	t.Run("with tracer provider", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		defer func() { _ = tp.Shutdown(context.Background()) }()

		p, err := NewProducer(
			WithBrokers("localhost:9092"),
			WithProducerTracerProvider(tp),
		)
		require.NoError(t, err)
		assert.Equal(t, tp, p.tracerProvider)
	})
}

func TestConsumer_Options(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		c, err := NewConsumer(WithConsumerBrokers("localhost:9092"))
		require.NoError(t, err)
		assert.NotNil(t, c.logger)
		assert.Equal(t, []string{"localhost:9092"}, c.brokers)
		assert.Empty(t, c.groupID)
		assert.Zero(t, c.maxRetries)
	})

	t.Run("requires at least one broker", func(t *testing.T) {
		_, err := NewConsumer()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one broker address")
	})

	t.Run("with group id", func(t *testing.T) {
		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithGroupID("my-group"),
		)
		require.NoError(t, err)
		assert.Equal(t, "my-group", c.groupID)
	})

	t.Run("with middleware", func(t *testing.T) {
		mw := func(next broker.Handler) broker.Handler {
			return next
		}
		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithMiddleware(mw),
		)
		require.NoError(t, err)
		assert.Len(t, c.middlewares, 1)
	})

	t.Run("with dlq", func(t *testing.T) {
		pub, err := NewProducer(WithBrokers("localhost:9092"))
		require.NoError(t, err)

		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithDLQ(pub, ".dlq"),
		)
		require.NoError(t, err)
		assert.Equal(t, pub, c.dlqPublisher)
		assert.Equal(t, ".dlq", c.dlqTopicSuffix)
	})

	t.Run("with retry", func(t *testing.T) {
		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithRetry(3, 100*time.Millisecond),
		)
		require.NoError(t, err)
		assert.Equal(t, 3, c.maxRetries)
		assert.Equal(t, 100*time.Millisecond, c.retryBackoff)
	})

	t.Run("with consumer logger", func(t *testing.T) {
		logger := platform.NopLogger()
		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithConsumerLogger(logger),
		)
		require.NoError(t, err)
		assert.NotNil(t, c.logger)
	})

	t.Run("with consumer tracer provider", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		defer func() { _ = tp.Shutdown(context.Background()) }()

		c, err := NewConsumer(
			WithConsumerBrokers("localhost:9092"),
			WithConsumerTracerProvider(tp),
		)
		require.NoError(t, err)
		assert.Equal(t, tp, c.tracerProvider)
	})
}

func TestKafkaHeaderCarrier(t *testing.T) {
	t.Run("set and get", func(t *testing.T) {
		carrier := kafkaHeaderCarrier{}
		carrier.Set("traceparent", "00-abc123-def456-01")
		assert.Equal(t, "00-abc123-def456-01", carrier.Get("traceparent"))
	})

	t.Run("get missing key returns empty", func(t *testing.T) {
		carrier := kafkaHeaderCarrier{}
		assert.Equal(t, "", carrier.Get("nonexistent"))
	})

	t.Run("set overwrites existing", func(t *testing.T) {
		carrier := kafkaHeaderCarrier{
			{Key: "key1", Value: []byte("value1")},
		}
		carrier.Set("key1", "value2")
		assert.Equal(t, "value2", carrier.Get("key1"))
		assert.Len(t, carrier, 1) // no duplicate
	})

	t.Run("keys returns all keys", func(t *testing.T) {
		carrier := kafkaHeaderCarrier{
			{Key: "traceparent", Value: []byte("v1")},
			{Key: "tracestate", Value: []byte("v2")},
		}
		keys := carrier.Keys()
		assert.ElementsMatch(t, []string{"traceparent", "tracestate"}, keys)
	})

	t.Run("keys on empty carrier", func(t *testing.T) {
		carrier := kafkaHeaderCarrier{}
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

	// Inject trace context into kafka headers.
	headers := InjectTraceContext(ctx, nil)
	assert.NotEmpty(t, headers, "headers should contain trace context")

	// Verify traceparent header was injected.
	var foundTraceparent bool
	for _, h := range headers {
		if h.Key == "traceparent" {
			foundTraceparent = true
			break
		}
	}
	assert.True(t, foundTraceparent, "traceparent header must be present")

	// Extract trace context from headers into a fresh context.
	extractedCtx := ExtractTraceContext(context.Background(), headers)
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

	existing := []kafkago.Header{
		{Key: "custom-header", Value: []byte("custom-value")},
	}

	headers := InjectTraceContext(ctx, existing)
	assert.True(t, len(headers) > 1, "should have both custom and trace headers")

	// Verify custom header is preserved.
	var foundCustom bool
	for _, h := range headers {
		if h.Key == "custom-header" && string(h.Value) == "custom-value" {
			foundCustom = true
		}
	}
	assert.True(t, foundCustom, "custom header must be preserved")
}

func TestKafkaHeadersToMap(t *testing.T) {
	t.Run("converts headers to map", func(t *testing.T) {
		headers := []kafkago.Header{
			{Key: "key1", Value: []byte("value1")},
			{Key: "key2", Value: []byte("value2")},
		}
		m := kafkaHeadersToMap(headers)
		assert.Equal(t, map[string]string{"key1": "value1", "key2": "value2"}, m)
	})

	t.Run("nil for empty headers", func(t *testing.T) {
		m := kafkaHeadersToMap(nil)
		assert.Nil(t, m)
	})
}

func TestProducer_StartStop(t *testing.T) {
	p, err := NewProducer(WithBrokers("localhost:9092"))
	require.NoError(t, err)

	ctx := context.Background()

	// Start creates the writer.
	err = p.Start(ctx)
	require.NoError(t, err)
	assert.NotNil(t, p.writer)

	// Double start returns error.
	err = p.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")

	// Stop closes the writer.
	err = p.Stop(ctx)
	require.NoError(t, err)
	assert.Nil(t, p.writer)

	// Double stop is a no-op.
	err = p.Stop(ctx)
	require.NoError(t, err)
}

func TestProducer_PublishWithoutStart(t *testing.T) {
	p, err := NewProducer(WithBrokers("localhost:9092"))
	require.NoError(t, err)

	err = p.Publish(context.Background(), "topic", "key", []byte("value"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")
}

func TestConsumer_StartStop(t *testing.T) {
	c, err := NewConsumer(
		WithConsumerBrokers("localhost:9092"),
		WithGroupID("test-group"),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Start is a no-op.
	err = c.Start(ctx)
	require.NoError(t, err)

	// Stop with no active readers is a no-op.
	err = c.Stop(ctx)
	require.NoError(t, err)
}
