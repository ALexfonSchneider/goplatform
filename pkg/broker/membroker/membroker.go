// Package membroker provides a synchronous, in-memory implementation of the
// broker.Publisher and broker.Subscriber interfaces. It is intended exclusively
// for unit tests where deterministic, single-goroutine message delivery is
// desirable.
//
// Key properties:
//   - Synchronous: Publish blocks until every registered handler has finished
//     processing the message.
//   - Deterministic: handlers are invoked in the order they were registered via
//     Subscribe (subscription order).
//   - Thread-safe: all operations are protected by a sync.RWMutex.
//   - Fan-out: multiple subscribers to the same topic each receive every message
//     published to that topic.
package membroker

import (
	"context"
	"sync"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
)

// MemBroker is a synchronous, in-memory implementation of broker.Publisher and
// broker.Subscriber. Messages published via Publish are delivered immediately
// and synchronously to all handlers registered for the target topic at the time
// of the call. MemBroker is safe for concurrent use.
type MemBroker struct {
	mu          sync.RWMutex
	handlers    map[string][]broker.Handler
	hooks       []broker.PublishHook
	middlewares []broker.Middleware
}

// New creates a new MemBroker ready for use. The returned broker has no
// subscribers, hooks, or middleware registered.
func New() *MemBroker {
	return &MemBroker{
		handlers: make(map[string][]broker.Handler),
	}
}

// Publish synchronously delivers a message to all handlers currently subscribed
// to the given topic. Before delivery, the payload is passed through all
// registered PublishHooks in registration order; each hook may transform the
// payload or abort publishing by returning an error. Middleware registered via
// WithMiddleware is applied to each handler before invocation.
//
// If there are no subscribers for the topic, Publish returns nil. If any
// handler returns an error, Publish returns that error immediately without
// calling remaining handlers.
func (b *MemBroker) Publish(ctx context.Context, topic string, key string, payload []byte) error {
	b.mu.RLock()
	hooks := make([]broker.PublishHook, len(b.hooks))
	copy(hooks, b.hooks)

	middlewares := make([]broker.Middleware, len(b.middlewares))
	copy(middlewares, b.middlewares)

	handlers := make([]broker.Handler, len(b.handlers[topic]))
	copy(handlers, b.handlers[topic])
	b.mu.RUnlock()

	// Run publish hooks in order, each transforming the payload for the next.
	transformedPayload := make([]byte, len(payload))
	copy(transformedPayload, payload)

	for _, hook := range hooks {
		var err error
		transformedPayload, err = hook(ctx, topic, key, transformedPayload)
		if err != nil {
			return err
		}
	}

	msg := broker.Message{
		Key:   []byte(key),
		Value: transformedPayload,
		Topic: topic,
	}

	for _, h := range handlers {
		wrapped := applyMiddleware(h, middlewares)
		if err := wrapped(ctx, msg); err != nil {
			return err
		}
	}

	return nil
}

// Subscribe registers a handler for the given topic. The handler will receive
// all messages published to the topic after this call. Multiple handlers may be
// registered for the same topic; they are invoked in registration order.
// Subscribe itself never returns an error for MemBroker; the error return
// exists to satisfy the broker.Subscriber interface.
func (b *MemBroker) Subscribe(_ context.Context, topic string, handler broker.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[topic] = append(b.handlers[topic], handler)

	return nil
}

// WithPublishHook appends a publish hook to the broker's hook pipeline. Hooks
// are executed in the order they are added. Each hook receives the payload
// returned by the previous hook and may transform it or return an error to
// abort publishing.
func (b *MemBroker) WithPublishHook(hook broker.PublishHook) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.hooks = append(b.hooks, hook)
}

// WithMiddleware appends one or more middleware to the broker's middleware
// chain. Middleware are applied to handlers in the order they are added
// (outermost first), following the same composition semantics as net/http
// middleware: the first middleware added is the first to execute and the last
// to see the result.
func (b *MemBroker) WithMiddleware(mw ...broker.Middleware) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.middlewares = append(b.middlewares, mw...)
}

// applyMiddleware wraps a handler with the given middleware chain. Middleware
// are applied in reverse order so that the first middleware in the slice is the
// outermost wrapper (executed first).
func applyMiddleware(h broker.Handler, mws []broker.Middleware) broker.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
