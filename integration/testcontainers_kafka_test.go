//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/broker/kafka"
)

// createKafkaTopic creates a topic on the Kafka cluster using kafka-go's Conn.
func createKafkaTopic(t *testing.T, brokerAddr, topic string) {
	t.Helper()

	conn, err := kafkago.Dial("tcp", brokerAddr)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()

	err = conn.CreateTopics(kafkago.TopicConfig{
		Topic:             topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
	})
	require.NoError(t, err)
}

func TestTC_Kafka_PublishConsume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ctr, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.5.0",
		tckafka.WithClusterID("test"),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	brokers, err := ctr.Brokers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, brokers)

	topic := "test-" + t.Name()
	groupID := "group-" + t.Name()

	createKafkaTopic(t, brokers[0], topic)

	// Create and start producer.
	producer, err := kafka.NewProducer(kafka.WithBrokers(brokers...))
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	defer func() { _ = producer.Stop(ctx) }()

	// Create and start consumer.
	consumer, err := kafka.NewConsumer(
		kafka.WithConsumerBrokers(brokers...),
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

	// Wait for consumer to join the group.
	time.Sleep(3 * time.Second)

	// Publish a message.
	err = producer.Publish(ctx, broker.Message{
		Topic: topic,
		Key:   []byte("key-1"),
		Value: []byte("value-1"),
	})
	require.NoError(t, err)

	// Wait for the handler to receive the message.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for message to be consumed")
	}

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []byte("key-1"), received.Key)
	assert.Equal(t, []byte("value-1"), received.Value)
	assert.Equal(t, topic, received.Topic)

	require.NoError(t, consumer.Stop(ctx))
}

func TestTC_Kafka_PublishBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ctr, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.5.0",
		tckafka.WithClusterID("test"),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	brokers, err := ctr.Brokers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, brokers)

	topic := "test-" + t.Name()
	groupID := "group-" + t.Name()

	createKafkaTopic(t, brokers[0], topic)

	// Create and start producer.
	producer, err := kafka.NewProducer(kafka.WithBrokers(brokers...))
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	defer func() { _ = producer.Stop(ctx) }()

	// Create and start consumer.
	consumer, err := kafka.NewConsumer(
		kafka.WithConsumerBrokers(brokers...),
		kafka.WithGroupID(groupID),
	)
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))

	const batchSize = 3
	var (
		receivedMsgs []broker.Message
		mu           sync.Mutex
		done         = make(chan struct{})
	)

	err = consumer.Subscribe(ctx, topic, func(_ context.Context, msg broker.Message) error {
		mu.Lock()
		defer mu.Unlock()
		receivedMsgs = append(receivedMsgs, msg)
		if len(receivedMsgs) == batchSize {
			close(done)
		}
		return nil
	})
	require.NoError(t, err)

	// Wait for consumer to join the group.
	time.Sleep(3 * time.Second)

	// Publish a batch.
	msgs := make([]broker.Message, batchSize)
	for i := range batchSize {
		msgs[i] = broker.Message{
			Topic: topic,
			Key:   []byte("batch-key"),
			Value: []byte("batch-value-" + string(rune('A'+i))),
		}
	}
	err = producer.PublishBatch(ctx, msgs)
	require.NoError(t, err)

	// Wait for all messages.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timeout waiting for batch messages to be consumed")
	}

	mu.Lock()
	defer mu.Unlock()

	assert.Len(t, receivedMsgs, batchSize)

	require.NoError(t, consumer.Stop(ctx))
}
