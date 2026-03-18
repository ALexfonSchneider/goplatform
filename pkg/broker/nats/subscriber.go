package nats

import (
	"context"
	"fmt"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time interface checks.
var (
	_ broker.Subscriber  = (*Subscriber)(nil)
	_ platform.Component = (*Subscriber)(nil)
)

// Subscriber implements broker.Subscriber and platform.Component using NATS.
// It supports both core NATS (ephemeral subscriptions) and JetStream (durable
// consumer) modes. Each call to Subscribe creates a NATS subscription that
// dispatches incoming messages to the provided handler. Subscriber is safe for
// concurrent use.
type Subscriber struct {
	mu   sync.Mutex
	conn *natsgo.Conn
	js   natsgo.JetStreamContext
	subs []*natsgo.Subscription
	wg   sync.WaitGroup

	url            string
	jetStream      bool
	durable        string
	queueGroup     string
	middlewares    []broker.Middleware
	dlqPublisher   broker.Publisher
	dlqTopicSuffix string
	maxRetries     int
	retryBackoff   time.Duration
	logger         platform.Logger
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer
	natsOpts       []natsgo.Option
}

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber)

// WithSubscriberURL sets the NATS server URL for the subscriber. The default is
// "nats://localhost:4222".
func WithSubscriberURL(url string) SubscriberOption {
	return func(s *Subscriber) {
		s.url = url
	}
}

// WithSubscriberJetStream enables JetStream mode for the subscriber. In
// JetStream mode, Subscribe creates a durable consumer that supports explicit
// message acknowledgement. In core mode (the default), Subscribe creates an
// ephemeral subscription with no delivery guarantees.
func WithSubscriberJetStream() SubscriberOption {
	return func(s *Subscriber) {
		s.jetStream = true
	}
}

// WithDurable sets the JetStream durable consumer name. This option is only
// meaningful when JetStream mode is enabled. A durable consumer survives
// restarts and resumes from the last acknowledged position.
func WithDurable(name string) SubscriberOption {
	return func(s *Subscriber) {
		s.durable = name
	}
}

// WithQueueGroup sets the queue group name for load balancing. When multiple
// subscribers share the same queue group, each message is delivered to only one
// member of the group. This works for both core NATS and JetStream modes.
func WithQueueGroup(group string) SubscriberOption {
	return func(s *Subscriber) {
		s.queueGroup = group
	}
}

// WithMiddleware appends one or more middleware to the subscriber's middleware
// chain. Middleware are applied to the handler in registration order (outermost
// first), following the same composition semantics as net/http middleware.
func WithMiddleware(mw ...broker.Middleware) SubscriberOption {
	return func(s *Subscriber) {
		s.middlewares = append(s.middlewares, mw...)
	}
}

// WithDLQ configures dead-letter queue routing. When a message exhausts all
// retries, it is published to a topic named "<original-topic><suffix>" using
// the provided publisher. If the DLQ publish fails, the message is Nak'd
// (JetStream) or logged (core) to prevent data loss.
func WithDLQ(publisher broker.Publisher, suffix string) SubscriberOption {
	return func(s *Subscriber) {
		s.dlqPublisher = publisher
		s.dlqTopicSuffix = suffix
	}
}

// WithRetry configures the retry policy for failed message handlers. Messages
// that fail processing are retried up to maxRetries times with the given
// backoff duration between attempts. If maxRetries is zero, no retries are
// attempted.
func WithRetry(maxRetries int, backoff time.Duration) SubscriberOption {
	return func(s *Subscriber) {
		s.maxRetries = maxRetries
		s.retryBackoff = backoff
	}
}

// WithSubscriberLogger sets the structured logger for the subscriber. If not
// set, a no-op logger is used.
func WithSubscriberLogger(l platform.Logger) SubscriberOption {
	return func(s *Subscriber) {
		s.logger = l
	}
}

// WithSubscriberTracerProvider sets the OpenTelemetry TracerProvider used by the
// subscriber to create spans for message processing. If not set, the global
// TracerProvider is used.
func WithSubscriberTracerProvider(tp trace.TracerProvider) SubscriberOption {
	return func(s *Subscriber) {
		s.tracerProvider = tp
	}
}

// WithSubscriberNATSOptions appends pass-through nats.go connection options
// that are forwarded directly to natsgo.Connect. This allows callers to
// configure TLS, authentication, reconnection behaviour, and other low-level
// NATS settings.
func WithSubscriberNATSOptions(opts ...natsgo.Option) SubscriberOption {
	return func(s *Subscriber) {
		s.natsOpts = append(s.natsOpts, opts...)
	}
}

// NewSubscriber creates a new Subscriber with the given options. The subscriber
// is not connected until Start is called. Returns an error if the configuration
// is invalid.
func NewSubscriber(opts ...SubscriberOption) (*Subscriber, error) {
	s := &Subscriber{
		url:    natsgo.DefaultURL,
		logger: platform.NopLogger(),
	}

	for _, opt := range opts {
		opt(s)
	}

	if s.tracerProvider == nil {
		s.tracerProvider = otel.GetTracerProvider()
	}
	s.tracer = s.tracerProvider.Tracer("github.com/ALexfonSchneider/goplatform/pkg/broker/nats")

	return s, nil
}

// Start connects to the NATS server. If JetStream mode is enabled, it also
// obtains a JetStream context. Start implements platform.Component.
func (s *Subscriber) Start(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil {
		return fmt.Errorf("nats: subscriber already started")
	}

	conn, err := natsgo.Connect(s.url, s.natsOpts...)
	if err != nil {
		return fmt.Errorf("nats: subscriber connect: %w", err)
	}

	s.conn = conn

	if s.jetStream {
		js, jsErr := conn.JetStream()
		if jsErr != nil {
			conn.Close()
			s.conn = nil
			return fmt.Errorf("nats: subscriber jetstream: %w", jsErr)
		}
		s.js = js
	}

	s.logger.Info("nats subscriber started", "url", s.url, "jetstream", s.jetStream)

	return nil
}

// Stop unsubscribes all active subscriptions, drains the connection, and waits
// for in-flight handlers to complete. Stop implements platform.Component.
func (s *Subscriber) Stop(_ context.Context) error {
	s.mu.Lock()
	subs := make([]*natsgo.Subscription, len(s.subs))
	copy(subs, s.subs)
	conn := s.conn
	s.mu.Unlock()

	if conn == nil {
		return nil
	}

	// Unsubscribe all active subscriptions.
	for _, sub := range subs {
		if err := sub.Drain(); err != nil {
			s.logger.Warn("nats: subscription drain failed", "subject", sub.Subject, "error", err)
		}
	}

	// Wait for in-flight handlers to complete.
	s.wg.Wait()

	// Drain the connection (flush + close).
	err := conn.Drain()

	s.mu.Lock()
	s.conn = nil
	s.js = nil
	s.subs = nil
	s.mu.Unlock()

	if err != nil {
		return fmt.Errorf("nats: subscriber drain: %w", err)
	}

	s.logger.Info("nats subscriber stopped")

	return nil
}

// Subscribe starts receiving messages on the given topic and calls handler for
// each message. The handler is wrapped with any registered middleware before
// invocation. In JetStream mode, a durable consumer is created with explicit
// acknowledgement. In core mode, an ephemeral subscription is created. If a
// queue group is configured, QueueSubscribe is used for load balancing.
// Subscribe implements broker.Subscriber.
func (s *Subscriber) Subscribe(_ context.Context, topic string, handler broker.Handler) error {
	s.mu.Lock()
	conn := s.conn
	js := s.js
	s.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("nats: subscriber not started")
	}

	// Apply middleware chain to the handler.
	wrapped := applyMiddleware(handler, s.middlewares)

	// Build the NATS message callback.
	msgHandler := func(msg *natsgo.Msg) {
		s.wg.Add(1)
		defer s.wg.Done()
		s.processMessage(msg, topic, wrapped)
	}

	var (
		sub *natsgo.Subscription
		err error
	)

	if js != nil {
		// JetStream subscribe.
		jsOpts := []natsgo.SubOpt{
			natsgo.ManualAck(),
		}
		if s.durable != "" {
			jsOpts = append(jsOpts, natsgo.Durable(s.durable))
		}
		if s.queueGroup != "" {
			sub, err = js.QueueSubscribe(topic, s.queueGroup, msgHandler, jsOpts...)
		} else {
			sub, err = js.Subscribe(topic, msgHandler, jsOpts...)
		}
	} else {
		// Core NATS subscribe.
		if s.queueGroup != "" {
			sub, err = conn.QueueSubscribe(topic, s.queueGroup, msgHandler)
		} else {
			sub, err = conn.Subscribe(topic, msgHandler)
		}
	}

	if err != nil {
		return fmt.Errorf("nats: subscribe to %q: %w", topic, err)
	}

	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()

	s.logger.Info("nats subscription started",
		"topic", topic,
		"queue_group", s.queueGroup,
		"durable", s.durable,
		"jetstream", s.jetStream,
	)

	return nil
}

// processMessage handles a single NATS message, including tracing, retry logic,
// and DLQ routing.
func (s *Subscriber) processMessage(natsMsg *natsgo.Msg, topic string, handler broker.Handler) {
	// Extract W3C trace context from NATS message headers.
	ctx := ExtractTraceContext(context.Background(), natsMsg.Header)

	ctx, span := s.tracer.Start(ctx, "nats.consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", topic),
		),
	)
	defer span.End()

	// Build the broker.Message from the NATS message.
	msg := broker.Message{
		Key:     []byte(natsMsg.Header.Get("key")),
		Value:   natsMsg.Data,
		Headers: natsHeadersToMap(natsMsg.Header),
		Topic:   topic,
	}

	// Attempt handler invocation with retry logic.
	var handlerErr error
	attempts := 1 + s.maxRetries // 1 initial attempt + retries
	for i := range attempts {
		handlerErr = handler(ctx, msg)
		if handlerErr == nil {
			break
		}

		s.logger.Warn("nats: handler failed",
			"topic", topic,
			"attempt", i+1,
			"max_attempts", attempts,
			"error", handlerErr,
		)

		// If this was the last attempt, stop retrying.
		if i == attempts-1 {
			break
		}

		// Wait before retrying, respecting context cancellation.
		select {
		case <-ctx.Done():
			span.RecordError(ctx.Err())
			span.SetStatus(codes.Error, "context cancelled during retry")
			return
		case <-time.After(s.retryBackoff):
		}
	}

	if handlerErr != nil {
		span.RecordError(handlerErr)

		// Route to DLQ if configured.
		if s.dlqPublisher != nil {
			dlqTopic := topic + s.dlqTopicSuffix
			dlqErr := s.dlqPublisher.Publish(ctx, dlqTopic, string(msg.Key), natsMsg.Data)
			if dlqErr != nil {
				// DLQ publish failed — Nak the message (JetStream) or log (core).
				span.RecordError(dlqErr)
				span.SetStatus(codes.Error, "dlq publish failed")
				s.logger.Error("nats: dlq publish failed",
					"topic", topic,
					"dlq_topic", dlqTopic,
					"handler_error", handlerErr,
					"dlq_error", dlqErr,
				)

				if s.js != nil {
					if nakErr := natsMsg.Nak(); nakErr != nil {
						s.logger.Error("nats: nak failed", "topic", topic, "error", nakErr)
					}
				}
				return
			}

			s.logger.Info("nats: message routed to dlq",
				"topic", topic,
				"dlq_topic", dlqTopic,
			)
		} else {
			// No DLQ configured.
			span.SetStatus(codes.Error, "handler failed, no dlq configured")
			s.logger.Error("nats: handler failed, no dlq configured",
				"topic", topic,
				"error", handlerErr,
			)

			if s.js != nil {
				if nakErr := natsMsg.Nak(); nakErr != nil {
					s.logger.Error("nats: nak failed", "topic", topic, "error", nakErr)
				}
				return
			}
		}
	} else {
		span.SetStatus(codes.Ok, "")
	}

	// Ack the message in JetStream mode.
	if s.js != nil {
		if ackErr := natsMsg.Ack(); ackErr != nil {
			s.logger.Error("nats: ack failed", "topic", topic, "error", ackErr)
		}
	}
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

// natsHeadersToMap converts natsgo.Header (http.Header) to a map[string]string.
// For headers with multiple values, only the first value is kept.
func natsHeadersToMap(header natsgo.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	m := make(map[string]string, len(header))
	for k, v := range header {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
