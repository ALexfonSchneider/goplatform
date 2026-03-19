//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/broker/kafka"
)

const kafkaBrokerAddr = "localhost:9092"

// TestBroker_KafkaPublishConsume publishes a message via a Kafka Producer and
// subscribes with a Consumer. It verifies the handler receives the message with
// the correct key and value.
func TestBroker_KafkaPublishConsume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := fmt.Sprintf("integ-pubsub-%s-%d", t.Name(), time.Now().UnixNano())

	// Create and start the producer.
	producer, err := kafka.NewProducer(kafka.WithBrokers(kafkaBrokerAddr))
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	defer func() { _ = producer.Stop(ctx) }()

	// Create and start the consumer.
	groupID := fmt.Sprintf("integ-group-%s-%d", t.Name(), time.Now().UnixNano())
	consumer, err := kafka.NewConsumer(
		kafka.WithConsumerBrokers(kafkaBrokerAddr),
		kafka.WithGroupID(groupID),
	)
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))

	var (
		received broker.Message
		mu       sync.Mutex
		done     = make(chan struct{})
	)

	err = consumer.Subscribe(ctx, topic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		received = msg
		close(done)
		return nil
	})
	require.NoError(t, err)

	// Wait for consumer to join the group and be ready.
	time.Sleep(2 * time.Second)

	// Publish a message.
	err = producer.Publish(ctx, broker.Message{Topic: topic, Key: []byte("test-key"), Value: []byte("test-value")})
	require.NoError(t, err)

	// Wait for the handler to receive the message.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for message to be consumed")
	}

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []byte("test-key"), received.Key)
	assert.Equal(t, []byte("test-value"), received.Value)
	assert.Equal(t, topic, received.Topic)

	require.NoError(t, consumer.Stop(ctx))
}

// TestBroker_DLQ subscribes with a handler that always fails. It configures
// 1 retry and a DLQ. It verifies the message appears on the DLQ topic after
// exhausting retries.
func TestBroker_DLQ(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	topic := fmt.Sprintf("integ-dlq-%s-%d", t.Name(), time.Now().UnixNano())
	dlqTopic := topic + ".dlq"

	// Create and start the producer (also used for DLQ publishing).
	producer, err := kafka.NewProducer(kafka.WithBrokers(kafkaBrokerAddr))
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	defer func() { _ = producer.Stop(ctx) }()

	// Create the failing consumer with retry and DLQ.
	failGroupID := fmt.Sprintf("integ-dlq-fail-%s-%d", t.Name(), time.Now().UnixNano())
	failConsumer, err := kafka.NewConsumer(
		kafka.WithConsumerBrokers(kafkaBrokerAddr),
		kafka.WithGroupID(failGroupID),
		kafka.WithRetry(1, 100*time.Millisecond),
		kafka.WithDLQ(producer, ".dlq"),
	)
	require.NoError(t, err)
	require.NoError(t, failConsumer.Start(ctx))

	err = failConsumer.Subscribe(ctx, topic, func(_ context.Context, _ broker.Message) error {
		return fmt.Errorf("intentional failure for DLQ test")
	})
	require.NoError(t, err)

	// Create a consumer for the DLQ topic.
	var (
		dlqReceived broker.Message
		mu          sync.Mutex
		dlqDone     = make(chan struct{})
	)

	dlqGroupID := fmt.Sprintf("integ-dlq-reader-%s-%d", t.Name(), time.Now().UnixNano())
	dlqConsumer, err := kafka.NewConsumer(
		kafka.WithConsumerBrokers(kafkaBrokerAddr),
		kafka.WithGroupID(dlqGroupID),
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

	// Wait for consumers to be ready.
	time.Sleep(2 * time.Second)

	// Publish a message that will fail processing and end up in the DLQ.
	err = producer.Publish(ctx, broker.Message{Topic: topic, Key: []byte("dlq-key"), Value: []byte("dlq-payload")})
	require.NoError(t, err)

	// Wait for the message to appear on the DLQ.
	select {
	case <-dlqDone:
	case <-ctx.Done():
		t.Fatal("timeout waiting for message on DLQ topic")
	}

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []byte("dlq-key"), dlqReceived.Key)
	assert.Equal(t, []byte("dlq-payload"), dlqReceived.Value)

	require.NoError(t, failConsumer.Stop(ctx))
	require.NoError(t, dlqConsumer.Stop(ctx))
}
