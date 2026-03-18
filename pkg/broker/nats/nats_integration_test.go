//go:build integration

package nats

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

const testNATSURL = "nats://localhost:4222"

func TestNATS_PublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test.pubsub.%d", time.Now().UnixNano())

	// Create and start publisher.
	p, err := NewPublisher(WithURL(testNATSURL))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create and start subscriber.
	var (
		received broker.Message
		mu       sync.Mutex
		done     = make(chan struct{})
	)

	s, err := NewSubscriber(WithSubscriberURL(testNATSURL))
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))

	err = s.Subscribe(ctx, topic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		received = msg
		close(done)
		return nil
	})
	require.NoError(t, err)

	// Wait briefly for the subscriber to connect.
	time.Sleep(500 * time.Millisecond)

	// Publish a message.
	err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte("key-1"), Value: []byte("hello nats")})
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
	assert.Equal(t, []byte("hello nats"), received.Value)
	assert.Equal(t, topic, received.Topic)

	require.NoError(t, s.Stop(ctx))
}

func TestNATS_JetStreamPublishSubscribe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test.js.pubsub.%d", time.Now().UnixNano())

	// Create and start JetStream publisher.
	p, err := NewPublisher(WithURL(testNATSURL), WithJetStream())
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create and start JetStream subscriber.
	var (
		received broker.Message
		mu       sync.Mutex
		done     = make(chan struct{})
	)

	s, err := NewSubscriber(
		WithSubscriberURL(testNATSURL),
		WithSubscriberJetStream(),
		WithDurable("test-durable"),
	)
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))

	err = s.Subscribe(ctx, topic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		received = msg
		close(done)
		return nil
	})
	require.NoError(t, err)

	// Wait briefly for the subscriber to connect.
	time.Sleep(500 * time.Millisecond)

	// Publish a message.
	err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte("js-key-1"), Value: []byte("hello jetstream")})
	require.NoError(t, err)

	// Wait for the message to be received.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for JetStream message")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []byte("js-key-1"), received.Key)
	assert.Equal(t, []byte("hello jetstream"), received.Value)
	assert.Equal(t, topic, received.Topic)

	require.NoError(t, s.Stop(ctx))
}

func TestNATS_QueueGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test.queue.%d", time.Now().UnixNano())

	// Create and start publisher.
	p, err := NewPublisher(WithURL(testNATSURL))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create two subscribers in the same queue group.
	var (
		count1 int
		count2 int
		mu     sync.Mutex
		wg     sync.WaitGroup
	)

	const numMessages = 10
	wg.Add(numMessages)

	handler1 := func(_ context.Context, _ broker.Message) error {
		mu.Lock()
		count1++
		mu.Unlock()
		wg.Done()
		return nil
	}

	handler2 := func(_ context.Context, _ broker.Message) error {
		mu.Lock()
		count2++
		mu.Unlock()
		wg.Done()
		return nil
	}

	s1, err := NewSubscriber(WithSubscriberURL(testNATSURL), WithQueueGroup("test-group"))
	require.NoError(t, err)
	require.NoError(t, s1.Start(ctx))

	s2, err := NewSubscriber(WithSubscriberURL(testNATSURL), WithQueueGroup("test-group"))
	require.NoError(t, err)
	require.NoError(t, s2.Start(ctx))

	require.NoError(t, s1.Subscribe(ctx, topic, handler1))
	require.NoError(t, s2.Subscribe(ctx, topic, handler2))

	time.Sleep(500 * time.Millisecond)

	// Publish messages.
	for i := range numMessages {
		err = p.Publish(ctx, broker.Message{Topic: topic, Key: []byte(fmt.Sprintf("key-%d", i)), Value: []byte(fmt.Sprintf("msg-%d", i))})
		require.NoError(t, err)
	}

	// Wait for all messages to be received.
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting for queue group messages")
	}

	mu.Lock()
	defer mu.Unlock()
	// Each message should be delivered to exactly one subscriber.
	assert.Equal(t, numMessages, count1+count2, "total messages must match")

	require.NoError(t, s1.Stop(ctx))
	require.NoError(t, s2.Stop(ctx))
}

func TestNATS_DLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test.dlq.%d", time.Now().UnixNano())
	dlqTopic := topic + ".dlq"

	// Create and start publisher (also used for DLQ publishing).
	p, err := NewPublisher(WithURL(testNATSURL))
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create subscriber that always fails.
	s, err := NewSubscriber(
		WithSubscriberURL(testNATSURL),
		WithRetry(2, 50*time.Millisecond),
		WithDLQ(p, ".dlq"),
	)
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))

	err = s.Subscribe(ctx, topic, func(_ context.Context, _ broker.Message) error {
		return fmt.Errorf("always fail")
	})
	require.NoError(t, err)

	// Subscribe to DLQ topic to verify the message arrives.
	var (
		dlqReceived broker.Message
		mu          sync.Mutex
		dlqDone     = make(chan struct{})
	)

	dlqSub, err := NewSubscriber(WithSubscriberURL(testNATSURL))
	require.NoError(t, err)
	require.NoError(t, dlqSub.Start(ctx))

	err = dlqSub.Subscribe(ctx, dlqTopic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		dlqReceived = msg
		close(dlqDone)
		return nil
	})
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

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

	require.NoError(t, s.Stop(ctx))
	require.NoError(t, dlqSub.Stop(ctx))
}

func TestNATS_TraceContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("test.trace.%d", time.Now().UnixNano())

	// Set up tracing.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer func() { _ = tp.Shutdown(ctx) }()

	tracer := tp.Tracer("test")

	// Create publisher with the test tracer provider.
	p, err := NewPublisher(
		WithURL(testNATSURL),
		WithPublisherTracerProvider(tp),
	)
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx))
	defer func() { _ = p.Stop(ctx) }()

	// Create subscriber with the test tracer provider.
	var (
		receivedTraceID trace.TraceID
		mu              sync.Mutex
		done            = make(chan struct{})
	)

	s, err := NewSubscriber(
		WithSubscriberURL(testNATSURL),
		WithSubscriberTracerProvider(tp),
	)
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))

	err = s.Subscribe(ctx, topic, func(ctx context.Context, _ broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		receivedTraceID = trace.SpanFromContext(ctx).SpanContext().TraceID()
		close(done)
		return nil
	})
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// Publish with an active span.
	publishCtx, span := tracer.Start(ctx, "publish-test")
	originalTraceID := span.SpanContext().TraceID()
	err = p.Publish(publishCtx, broker.Message{Topic: topic, Key: []byte("trace-key"), Value: []byte("trace-payload")})
	span.End()
	require.NoError(t, err)

	// Wait for the subscriber to process.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for traced message")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, originalTraceID, receivedTraceID,
		"subscriber should receive the same trace ID as the publisher")

	require.NoError(t, s.Stop(ctx))
}
