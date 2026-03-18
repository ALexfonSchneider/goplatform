package nats

import (
	"context"
	"fmt"
	"sync"

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
	_ broker.Publisher   = (*Publisher)(nil)
	_ platform.Component = (*Publisher)(nil)
)

// Publisher implements broker.Publisher and platform.Component using NATS.
// It supports both core NATS (fire-and-forget) and JetStream (persistent)
// publish modes. The connection is established lazily during Start and drained
// during Stop. Publisher is safe for concurrent use.
type Publisher struct {
	mu   sync.RWMutex
	conn *natsgo.Conn
	js   natsgo.JetStreamContext

	url            string
	jetStream      bool
	hooks          []broker.PublishHook
	logger         platform.Logger
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer
	natsOpts       []natsgo.Option
}

// PublisherOption configures a Publisher.
type PublisherOption func(*Publisher)

// WithURL sets the NATS server URL for the publisher. The default is
// "nats://localhost:4222".
func WithURL(url string) PublisherOption {
	return func(p *Publisher) {
		p.url = url
	}
}

// WithJetStream enables JetStream mode for the publisher. In JetStream mode,
// Publish uses JetStream publish with acknowledgement and message deduplication
// via MsgId. In core mode (the default), Publish uses basic NATS publish which
// is fire-and-forget with no delivery guarantee.
func WithJetStream() PublisherOption {
	return func(p *Publisher) {
		p.jetStream = true
	}
}

// WithPublishHook appends one or more publish hooks to the publisher's hook
// pipeline. Hooks are executed in registration order before each message is
// sent. Each hook receives the payload returned by the previous hook and may
// transform it or abort publishing by returning an error.
func WithPublishHook(hooks ...broker.PublishHook) PublisherOption {
	return func(p *Publisher) {
		p.hooks = append(p.hooks, hooks...)
	}
}

// WithPublisherLogger sets the structured logger for the publisher. If not set,
// a no-op logger is used.
func WithPublisherLogger(l platform.Logger) PublisherOption {
	return func(p *Publisher) {
		p.logger = l
	}
}

// WithPublisherTracerProvider sets the OpenTelemetry TracerProvider used by the
// publisher to create spans for publish operations. If not set, the global
// TracerProvider is used.
func WithPublisherTracerProvider(tp trace.TracerProvider) PublisherOption {
	return func(p *Publisher) {
		p.tracerProvider = tp
	}
}

// WithNATSOptions appends pass-through nats.go connection options that are
// forwarded directly to natsgo.Connect. This allows callers to configure TLS,
// authentication, reconnection behaviour, and other low-level NATS settings.
func WithNATSOptions(opts ...natsgo.Option) PublisherOption {
	return func(p *Publisher) {
		p.natsOpts = append(p.natsOpts, opts...)
	}
}

// NewPublisher creates a new Publisher with the given options. The publisher is
// not connected until Start is called. Returns an error if the configuration is
// invalid.
func NewPublisher(opts ...PublisherOption) (*Publisher, error) {
	p := &Publisher{
		url:    natsgo.DefaultURL,
		logger: platform.NopLogger(),
	}

	for _, opt := range opts {
		opt(p)
	}

	if p.tracerProvider == nil {
		p.tracerProvider = otel.GetTracerProvider()
	}
	p.tracer = p.tracerProvider.Tracer("github.com/ALexfonSchneider/goplatform/pkg/broker/nats")

	return p, nil
}

// Start connects to the NATS server. If JetStream mode is enabled, it also
// obtains a JetStream context. Start implements platform.Component.
func (p *Publisher) Start(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		return fmt.Errorf("nats: publisher already started")
	}

	conn, err := natsgo.Connect(p.url, p.natsOpts...)
	if err != nil {
		return fmt.Errorf("nats: publisher connect: %w", err)
	}

	p.conn = conn

	if p.jetStream {
		js, jsErr := conn.JetStream()
		if jsErr != nil {
			conn.Close()
			p.conn = nil
			return fmt.Errorf("nats: publisher jetstream: %w", jsErr)
		}
		p.js = js
	}

	p.logger.Info("nats publisher started", "url", p.url, "jetstream", p.jetStream)

	return nil
}

// Stop drains and closes the NATS connection. Drain flushes all buffered data
// and waits for processing to complete before closing. Stop implements
// platform.Component.
func (p *Publisher) Stop(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return nil
	}

	err := p.conn.Drain()
	p.conn = nil
	p.js = nil

	if err != nil {
		return fmt.Errorf("nats: publisher drain: %w", err)
	}

	p.logger.Info("nats publisher stopped")

	return nil
}

// Publish sends a message to the given topic. Before sending, the payload is
// passed through all registered PublishHooks in order. W3C trace context is
// injected into the NATS message headers so that subscribers can link their
// spans to the publisher's trace. In JetStream mode, the message is published
// with acknowledgement and MsgId is set to the key for deduplication. In core
// mode, the message is published fire-and-forget. The key is always stored in
// the "key" header since NATS does not have a native per-message key concept.
// Publish implements broker.Publisher.
func (p *Publisher) Publish(ctx context.Context, topic string, key string, payload []byte) error {
	ctx, span := p.tracer.Start(ctx, "nats.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.nats.message.key", key),
		),
	)
	defer span.End()

	p.mu.RLock()
	conn := p.conn
	js := p.js
	p.mu.RUnlock()

	if conn == nil {
		span.SetStatus(codes.Error, "publisher not started")
		return fmt.Errorf("nats: publisher not started")
	}

	// Run publish hooks pipeline.
	transformed := make([]byte, len(payload))
	copy(transformed, payload)

	for _, hook := range p.hooks {
		var err error
		transformed, err = hook(ctx, topic, key, transformed)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "publish hook failed")
			return fmt.Errorf("nats: publish hook: %w", err)
		}
	}

	// Inject W3C trace context into NATS message headers.
	header := InjectTraceContext(ctx, nil)

	// Store the key in a header since NATS has no native message key.
	header.Set("key", key)

	msg := &natsgo.Msg{
		Subject: topic,
		Data:    transformed,
		Header:  header,
	}

	if js != nil {
		// JetStream publish with ack and dedup via MsgId.
		_, err := js.PublishMsg(msg, natsgo.MsgId(key))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "jetstream publish failed")
			return fmt.Errorf("nats: jetstream publish to %q: %w", topic, err)
		}
	} else {
		// Core NATS publish (fire-and-forget).
		if err := conn.PublishMsg(msg); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "publish failed")
			return fmt.Errorf("nats: publish to %q: %w", topic, err)
		}
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
