package membroker

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
)

func TestMemBroker_PublishSubscribe(t *testing.T) {
	b := New()
	ctx := context.Background()

	var received broker.Message

	err := b.Subscribe(ctx, "orders", func(_ context.Context, msg broker.Message) error {
		received = msg
		return nil
	})
	require.NoError(t, err)

	err = b.Publish(ctx, "orders", "order-1", []byte("payload-data"))
	require.NoError(t, err)

	assert.Equal(t, []byte("order-1"), received.Key)
	assert.Equal(t, []byte("payload-data"), received.Value)
	assert.Equal(t, "orders", received.Topic)
}

func TestMemBroker_PublishHook(t *testing.T) {
	b := New()
	ctx := context.Background()

	// Add a hook that uppercases the payload.
	b.WithPublishHook(func(_ context.Context, _ string, _ string, payload []byte) ([]byte, error) {
		return bytes.ToUpper(payload), nil
	})

	var received broker.Message

	err := b.Subscribe(ctx, "events", func(_ context.Context, msg broker.Message) error {
		received = msg
		return nil
	})
	require.NoError(t, err)

	err = b.Publish(ctx, "events", "evt-1", []byte("hello world"))
	require.NoError(t, err)

	assert.Equal(t, []byte("HELLO WORLD"), received.Value)
}

func TestMemBroker_Middleware(t *testing.T) {
	b := New()
	ctx := context.Background()

	var log []string

	// First middleware added runs first (outermost).
	b.WithMiddleware(func(next broker.Handler) broker.Handler {
		return func(ctx context.Context, msg broker.Message) error {
			log = append(log, "mw1-before")
			err := next(ctx, msg)
			log = append(log, "mw1-after")
			return err
		}
	})

	// Second middleware added runs second (inner).
	b.WithMiddleware(func(next broker.Handler) broker.Handler {
		return func(ctx context.Context, msg broker.Message) error {
			log = append(log, "mw2-before")
			err := next(ctx, msg)
			log = append(log, "mw2-after")
			return err
		}
	})

	err := b.Subscribe(ctx, "topic", func(_ context.Context, _ broker.Message) error {
		log = append(log, "handler")
		return nil
	})
	require.NoError(t, err)

	err = b.Publish(ctx, "topic", "k", []byte("v"))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"mw1-before",
		"mw2-before",
		"handler",
		"mw2-after",
		"mw1-after",
	}, log)
}

func TestMemBroker_MultipleSubscribers(t *testing.T) {
	b := New()
	ctx := context.Background()

	var received1, received2 broker.Message

	err := b.Subscribe(ctx, "notifications", func(_ context.Context, msg broker.Message) error {
		received1 = msg
		return nil
	})
	require.NoError(t, err)

	err = b.Subscribe(ctx, "notifications", func(_ context.Context, msg broker.Message) error {
		received2 = msg
		return nil
	})
	require.NoError(t, err)

	err = b.Publish(ctx, "notifications", "n-1", []byte("alert"))
	require.NoError(t, err)

	assert.Equal(t, []byte("alert"), received1.Value)
	assert.Equal(t, []byte("alert"), received2.Value)
	assert.Equal(t, "notifications", received1.Topic)
	assert.Equal(t, "notifications", received2.Topic)
}

func TestMemBroker_NoSubscribers(t *testing.T) {
	b := New()
	ctx := context.Background()

	err := b.Publish(ctx, "empty-topic", "k", []byte("data"))
	assert.NoError(t, err)
}
