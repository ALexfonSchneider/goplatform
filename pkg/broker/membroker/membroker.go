// Package membroker provides a synchronous, in-memory implementation of the
// broker.Publisher and broker.Subscriber interfaces for unit tests.
package membroker

import (
	"context"
	"sync"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
)

// MemBroker is a synchronous, in-memory implementation of broker.Publisher and
// broker.Subscriber. Messages are delivered immediately and synchronously to
// all handlers registered for the target topic. Safe for concurrent use.
type MemBroker struct {
	mu          sync.RWMutex
	handlers    map[string][]broker.Handler
	hooks       []broker.PublishHook
	middlewares []broker.Middleware
}

// New creates a new MemBroker ready for use.
func New() *MemBroker {
	return &MemBroker{
		handlers: make(map[string][]broker.Handler),
	}
}

// Publish synchronously delivers a message to all handlers subscribed to
// msg.Topic. Hooks are executed first and may modify the message.
func (b *MemBroker) Publish(ctx context.Context, msg broker.Message) error {
	b.mu.RLock()
	hooks := make([]broker.PublishHook, len(b.hooks))
	copy(hooks, b.hooks)
	middlewares := make([]broker.Middleware, len(b.middlewares))
	copy(middlewares, b.middlewares)
	handlers := make([]broker.Handler, len(b.handlers[msg.Topic]))
	copy(handlers, b.handlers[msg.Topic])
	b.mu.RUnlock()

	// Run publish hooks — each may modify the message.
	for _, hook := range hooks {
		if err := hook(ctx, &msg); err != nil {
			return err
		}
	}

	for _, h := range handlers {
		wrapped := applyMiddleware(h, middlewares)
		if err := wrapped(ctx, msg); err != nil {
			return err
		}
	}

	return nil
}

// PublishBatch publishes multiple messages sequentially.
func (b *MemBroker) PublishBatch(ctx context.Context, msgs []broker.Message) error {
	for _, msg := range msgs {
		if err := b.Publish(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

// Subscribe registers a handler for the given topic.
func (b *MemBroker) Subscribe(_ context.Context, topic string, handler broker.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[topic] = append(b.handlers[topic], handler)
	return nil
}

// WithPublishHook appends a publish hook to the broker's hook pipeline.
func (b *MemBroker) WithPublishHook(hook broker.PublishHook) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = append(b.hooks, hook)
}

// WithMiddleware appends middleware to the broker's middleware chain.
func (b *MemBroker) WithMiddleware(mw ...broker.Middleware) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.middlewares = append(b.middlewares, mw...)
}

func applyMiddleware(h broker.Handler, mws []broker.Middleware) broker.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
