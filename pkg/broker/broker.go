// Package broker defines interfaces for asynchronous message publishing and
// subscribing. It provides a minimal, transport-agnostic contract that can be
// backed by Kafka, NATS, an in-memory implementation for tests, or any other
// message broker.
//
// The key abstractions are:
//   - Publisher: sends messages to a named topic.
//   - Subscriber: registers a Handler that is invoked for each message on a topic.
//   - Handler / Message: the callback signature and the envelope it receives.
//   - Middleware: wraps a Handler to inject cross-cutting concerns (logging,
//     metrics, retries, dead-letter routing, etc.), following the same
//     composition pattern as net/http middleware.
//   - PublishHook: intercepts outgoing messages before they leave the publisher,
//     allowing payload transformation, enrichment, or validation.
package broker

import "context"

// Publisher publishes messages to a named topic. Implementations may be
// synchronous (blocking until the broker acknowledges) or asynchronous
// (returning once the message is buffered locally). The key parameter is used
// for partitioning: messages with the same key are guaranteed to land in the
// same partition when the underlying broker supports it.
type Publisher interface {
	Publish(ctx context.Context, topic string, key string, payload []byte) error
}

// Subscriber registers handlers for incoming messages on a topic. A single
// Subscriber may support multiple concurrent subscriptions. Implementations
// must be safe for concurrent use.
type Subscriber interface {
	Subscribe(ctx context.Context, topic string, handler Handler) error
}

// Handler processes a single broker message. Returning a non-nil error signals
// the broker that the message was not processed successfully; the broker may
// then retry delivery or route the message to a dead-letter queue, depending
// on its configuration.
type Handler func(ctx context.Context, msg Message) error

// Message represents a single message received from or published to a broker.
// Fields such as Partition and Offset are populated by the broker on the
// consume path and may be zero-valued on the publish path.
type Message struct {
	// Key is the partitioning key of the message.
	Key []byte

	// Value is the message payload.
	Value []byte

	// Headers carries optional key-value metadata associated with the message.
	Headers map[string]string

	// Topic is the name of the topic this message belongs to.
	Topic string

	// Partition is the partition index within the topic (consume path only).
	Partition int

	// Offset is the broker-assigned offset of the message (consume path only).
	Offset int64
}

// Middleware wraps a Handler to add cross-cutting behavior such as logging,
// tracing, or error handling. Middleware functions are composed in the same
// style as net/http middleware: the outermost middleware is called first and
// must invoke next to continue the chain.
type Middleware func(next Handler) Handler

// PublishHook intercepts messages before they are published. A hook receives
// the original payload and returns a (possibly transformed) payload. Returning
// a non-nil error aborts the publish operation. When multiple hooks are
// registered, each hook receives the payload returned by the previous one,
// forming a pipeline.
type PublishHook func(ctx context.Context, topic string, key string, payload []byte) ([]byte, error)
