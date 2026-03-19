//go:build integration

package integration

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/broker/membroker"
)

// TestMemBroker_PubSub verifies synchronous in-memory publish/subscribe.
func TestMemBroker_PubSub(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()
	mb := membroker.New()

	var received broker.Message
	err := mb.Subscribe(ctx, "orders", func(_ context.Context, msg broker.Message) error {
		received = msg
		return nil
	})
	require.NoError(t, err)

	err = mb.Publish(ctx, broker.Message{
		Topic: "orders",
		Key:   []byte("order-1"),
		Value: []byte(`{"id":"order-1"}`),
	})
	require.NoError(t, err)

	assert.Equal(t, []byte("order-1"), received.Key)
	assert.Equal(t, []byte(`{"id":"order-1"}`), received.Value)
}

// TestMemBroker_PublishBatch verifies batch publishing delivers all messages.
func TestMemBroker_PublishBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()
	mb := membroker.New()

	var count atomic.Int32
	err := mb.Subscribe(ctx, "events", func(_ context.Context, _ broker.Message) error {
		count.Add(1)
		return nil
	})
	require.NoError(t, err)

	msgs := []broker.Message{
		{Topic: "events", Key: []byte("1"), Value: []byte("a")},
		{Topic: "events", Key: []byte("2"), Value: []byte("b")},
		{Topic: "events", Key: []byte("3"), Value: []byte("c")},
	}

	err = mb.PublishBatch(ctx, msgs)
	require.NoError(t, err)

	assert.Equal(t, int32(3), count.Load(), "all 3 messages should be delivered")
}

// TestMemBroker_PublishHook verifies that publish hooks can modify messages.
func TestMemBroker_PublishHook(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()
	mb := membroker.New()

	// Add a hook that injects a correlation ID header.
	mb.WithPublishHook(func(_ context.Context, msg *broker.Message) error {
		if msg.Headers == nil {
			msg.Headers = make(map[string]string)
		}
		msg.Headers["x-correlation-id"] = "hook-injected-123"
		return nil
	})

	var received broker.Message
	err := mb.Subscribe(ctx, "hooked", func(_ context.Context, msg broker.Message) error {
		received = msg
		return nil
	})
	require.NoError(t, err)

	err = mb.Publish(ctx, broker.Message{
		Topic: "hooked",
		Key:   []byte("k"),
		Value: []byte("v"),
	})
	require.NoError(t, err)

	assert.Equal(t, "hook-injected-123", received.Headers["x-correlation-id"],
		"publish hook should inject the header")
}

// TestMemBroker_Middleware verifies that consumer middleware wraps the handler.
func TestMemBroker_Middleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()
	mb := membroker.New()

	var middlewareCalled bool
	mb.WithMiddleware(func(next broker.Handler) broker.Handler {
		return func(ctx context.Context, msg broker.Message) error {
			middlewareCalled = true
			return next(ctx, msg)
		}
	})

	var handlerCalled bool
	err := mb.Subscribe(ctx, "mw-topic", func(_ context.Context, _ broker.Message) error {
		handlerCalled = true
		return nil
	})
	require.NoError(t, err)

	err = mb.Publish(ctx, broker.Message{
		Topic: "mw-topic",
		Key:   []byte("k"),
		Value: []byte("v"),
	})
	require.NoError(t, err)

	assert.True(t, middlewareCalled, "middleware should have been called")
	assert.True(t, handlerCalled, "handler should have been called")
}

// TestMemBroker_MultipleSubscribers verifies that multiple subscribers on the
// same topic all receive the message.
func TestMemBroker_MultipleSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx := context.Background()
	mb := membroker.New()

	var count atomic.Int32

	for range 3 {
		err := mb.Subscribe(ctx, "multi", func(_ context.Context, _ broker.Message) error {
			count.Add(1)
			return nil
		})
		require.NoError(t, err)
	}

	err := mb.Publish(ctx, broker.Message{
		Topic: "multi",
		Key:   []byte("k"),
		Value: []byte("v"),
	})
	require.NoError(t, err)

	assert.Equal(t, int32(3), count.Load(), "all 3 subscribers should receive the message")
}
