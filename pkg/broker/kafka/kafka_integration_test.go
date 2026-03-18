//go:build integration

package kafka

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
)

const testBrokerAddr = "localhost:9092"

func TestKafka_PublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test-pubsub-%d", time.Now().UnixNano())

	// Create and start producer.
	p, err := NewProducer(WithBrokers(testBrokerAddr))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create and start consumer.
	var (
		received broker.Message
		mu       sync.Mutex
		done     = make(chan struct{})
	)

	c, err := NewConsumer(
		WithConsumerBrokers(testBrokerAddr),
		WithGroupID("test-pubsub-group"),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))

	err = c.Subscribe(ctx, topic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		received = msg
		close(done)
		return nil
	})
	require.NoError(t, err)

	// Wait briefly for the consumer to connect.
	time.Sleep(2 * time.Second)

	// Publish a message.
	err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte("key-1"), Value: []byte("hello kafka")})
	require.NoError(t, err)

	// Wait for the message to be received.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []byte("key-1"), received.Key)
	assert.Equal(t, []byte("hello kafka"), received.Value)
	assert.Equal(t, topic, received.Topic)

	require.NoError(t, c.Stop(ctx))
}

func TestKafka_StopGraceful(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test-graceful-%d", time.Now().UnixNano())

	// Create and start producer.
	p, err := NewProducer(WithBrokers(testBrokerAddr))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create consumer with a slow handler.
	var (
		handlerCompleted bool
		mu               sync.Mutex
		handlerStarted   = make(chan struct{})
	)

	c, err := NewConsumer(
		WithConsumerBrokers(testBrokerAddr),
		WithGroupID("test-graceful-group"),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))

	err = c.Subscribe(ctx, topic, func(_ context.Context, _ broker.Message) error {
		close(handlerStarted)
		time.Sleep(2 * time.Second)
		mu.Lock()
		handlerCompleted = true
		mu.Unlock()
		return nil
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Publish a message.
	err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte("key"), Value: []byte("slow-message")})
	require.NoError(t, err)

	// Wait for handler to start processing.
	select {
	case <-handlerStarted:
	case <-ctx.Done():
		t.Fatal("timeout waiting for handler to start")
	}

	// Stop while handler is processing — should wait for completion.
	require.NoError(t, c.Stop(ctx))

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, handlerCompleted, "handler should complete before Stop returns")
}

func TestKafka_DLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test-dlq-%d", time.Now().UnixNano())
	dlqTopic := topic + ".dlq"

	// Create and start producer (also used for DLQ publishing).
	p, err := NewProducer(WithBrokers(testBrokerAddr))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create consumer that always fails.
	c, err := NewConsumer(
		WithConsumerBrokers(testBrokerAddr),
		WithGroupID("test-dlq-group"),
		WithRetry(2, 100*time.Millisecond),
		WithDLQ(p, ".dlq"),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))

	err = c.Subscribe(ctx, topic, func(_ context.Context, _ broker.Message) error {
		return fmt.Errorf("always fail")
	})
	require.NoError(t, err)

	// Subscribe to DLQ topic to verify the message arrives.
	var (
		dlqReceived broker.Message
		mu          sync.Mutex
		dlqDone     = make(chan struct{})
	)

	dlqConsumer, err := NewConsumer(
		WithConsumerBrokers(testBrokerAddr),
		WithGroupID("test-dlq-reader-group"),
	)
	require.NoError(t, err)
	require.NoError(t, dlqConsumer.Start(ctx))

	err = dlqConsumer.Subscribe(ctx, dlqTopic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		dlqReceived = msg
		close(dlqDone)
		return nil
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Publish a message that will fail processing.
	err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte("fail-key"), Value: []byte("fail-payload")})
	require.NoError(t, err)

	// Wait for the message to appear on the DLQ.
	select {
	case <-dlqDone:
	case <-ctx.Done():
		t.Fatal("timeout waiting for DLQ message")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []byte("fail-key"), dlqReceived.Key)
	assert.Equal(t, []byte("fail-payload"), dlqReceived.Value)

	require.NoError(t, c.Stop(ctx))
	require.NoError(t, dlqConsumer.Stop(ctx))
}

func TestKafka_TraceContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test-trace-%d", time.Now().UnixNano())

	// Set up tracing.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(ctx) }()

	tracer := tp.Tracer("test")

	// Create producer with the test tracer provider.
	p, err := NewProducer(
		WithBrokers(testBrokerAddr),
		WithProducerTracerProvider(tp),
	)
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create consumer with the test tracer provider.
	var (
		receivedTraceID trace.TraceID
		mu              sync.Mutex
		done            = make(chan struct{})
	)

	c, err := NewConsumer(
		WithConsumerBrokers(testBrokerAddr),
		WithGroupID("test-trace-group"),
		WithConsumerTracerProvider(tp),
	)
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))

	err = c.Subscribe(ctx, topic, func(ctx context.Context, _ broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		receivedTraceID = trace.SpanFromContext(ctx).SpanContext().TraceID()
		close(done)
		return nil
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	// Publish with an active span.
	publishCtx, span := tracer.Start(ctx, "publish-test")
	originalTraceID := span.SpanContext().TraceID()
	err = p.Publish(publishCtx, broker.Message{Topic: topic, Key: []byte("trace-key"), Value: []byte("trace-payload")})
	span.End()
	require.NoError(t, err)

	// Wait for the consumer to process.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for traced message")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, originalTraceID, receivedTraceID,
		"consumer should receive the same trace ID as the producer")

	require.NoError(t, c.Stop(ctx))
}
