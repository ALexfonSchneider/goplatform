// Package broker defines interfaces for asynchronous message publishing and
// subscribing. It provides a minimal, transport-agnostic contract that can be
// backed by Kafka, NATS, an in-memory implementation for tests, or any other
// message broker.
package broker

import "context"

// Publisher publishes messages to a broker. Implementations may be synchronous
// (blocking until the broker acknowledges) or asynchronous (returning once the
// message is buffered locally).
type Publisher interface {
	// Publish sends a single message. Message.Topic must be set.
	Publish(ctx context.Context, msg Message) error

	// PublishBatch sends multiple messages atomically when the broker supports it.
	// For brokers without native batch support, messages are sent sequentially.
	// Returns on the first error; already-sent messages are not rolled back.
	PublishBatch(ctx context.Context, msgs []Message) error
}

// Subscriber registers handlers for incoming messages on a topic. A single
// Subscriber may support multiple concurrent subscriptions. Implementations
// must be safe for concurrent use.
type Subscriber interface {
	Subscribe(ctx context.Context, topic string, handler Handler) error
}

// Handler processes a single broker message. Returning a non-nil error signals
// the broker that the message was not processed successfully; the broker may
// then retry delivery or route the message to a dead-letter queue.
type Handler func(ctx context.Context, msg Message) error

// Message represents a single message received from or published to a broker.
// On the publish path, Topic, Key, Value, and Headers are set by the caller.
// On the consume path, Partition and Offset are populated by the broker.
type Message struct {
	// Topic is the destination topic/subject.
	Topic string

	// Key is the partitioning key. Used by Kafka for partition routing.
	// NATS implementations may store it as a header or ignore it.
	Key []byte

	// Value is the message payload.
	Value []byte

	// Headers carries optional key-value metadata associated with the message.
	// Used for trace propagation, content-type, correlation IDs, etc.
	Headers map[string]string

	// Partition is the partition index (consume path only, Kafka-specific).
	Partition int

	// Offset is the broker-assigned offset (consume path only, Kafka-specific).
	Offset int64
}

// Middleware wraps a Handler to add cross-cutting behavior such as logging,
// tracing, or error handling. Middleware functions are composed in the same
// style as net/http middleware.
type Middleware func(next Handler) Handler

// PublishHook intercepts messages before they are published. A hook receives
// a pointer to the Message and may modify any field (payload, headers, key).
// Returning a non-nil error aborts the publish operation. When multiple hooks
// are registered, they execute in order, each seeing the modifications of
// previous hooks.
type PublishHook func(ctx context.Context, msg *Message) error
