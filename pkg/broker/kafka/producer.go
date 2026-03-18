package kafka

import (
	"context"
	"fmt"
	"sync"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ALexfonSchneider/goplatform/pkg/broker"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time interface checks.
var (
	_ broker.Publisher   = (*Producer)(nil)
	_ platform.Component = (*Producer)(nil)
)

// Producer implements broker.Publisher and platform.Component. It wraps
// kafka-go's Writer for producing messages to Kafka topics. The Writer is
// created lazily during Start and closed during Stop.
type Producer struct {
	mu     sync.RWMutex
	writer *kafkago.Writer

	brokers        []string
	hooks          []broker.PublishHook
	logger         platform.Logger
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer
}

// ProducerOption configures a Producer.
type ProducerOption func(*Producer)

// WithBrokers sets the Kafka broker addresses for the producer. At least one
// address must be provided before Start is called.
func WithBrokers(addrs ...string) ProducerOption {
	return func(p *Producer) {
		p.brokers = addrs
	}
}

// WithPublishHook appends one or more publish hooks to the producer's hook
// pipeline. Hooks are executed in registration order before each message is
// sent. Each hook receives the payload returned by the previous hook and may
// transform it or abort publishing by returning an error.
func WithPublishHook(hooks ...broker.PublishHook) ProducerOption {
	return func(p *Producer) {
		p.hooks = append(p.hooks, hooks...)
	}
}

// WithProducerLogger sets the structured logger for the producer. If not set,
// a no-op logger is used.
func WithProducerLogger(l platform.Logger) ProducerOption {
	return func(p *Producer) {
		p.logger = l
	}
}

// WithProducerTracerProvider sets the OpenTelemetry TracerProvider used by the
// producer to create spans for publish operations. If not set, the global
// TracerProvider is used.
func WithProducerTracerProvider(tp trace.TracerProvider) ProducerOption {
	return func(p *Producer) {
		p.tracerProvider = tp
	}
}

// NewProducer creates a new Producer with the given options. The producer is not
// connected until Start is called. Returns an error if the configuration is
// invalid (e.g., no broker addresses provided).
func NewProducer(opts ...ProducerOption) (*Producer, error) {
	p := &Producer{
		logger: platform.NopLogger(),
	}

	for _, opt := range opts {
		opt(p)
	}

	if len(p.brokers) == 0 {
		return nil, fmt.Errorf("kafka: producer requires at least one broker address")
	}

	if p.tracerProvider == nil {
		p.tracerProvider = otel.GetTracerProvider()
	}
	p.tracer = p.tracerProvider.Tracer("github.com/ALexfonSchneider/goplatform/pkg/broker/kafka")

	return p, nil
}

// Start initializes the Kafka writer. The kafka-go Writer connects lazily on
// the first write, so Start itself does not block on network I/O. Start
// implements platform.Component.
func (p *Producer) Start(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.writer != nil {
		return fmt.Errorf("kafka: producer already started")
	}

	p.writer = &kafkago.Writer{
		Addr:                   kafkago.TCP(p.brokers...),
		Balancer:               &kafkago.LeastBytes{},
		AllowAutoTopicCreation: true,
	}

	p.logger.Info("kafka producer started", "brokers", p.brokers)

	return nil
}

// Stop flushes pending messages and closes the underlying writer. Stop
// implements platform.Component.
func (p *Producer) Stop(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.writer == nil {
		return nil
	}

	err := p.writer.Close()
	p.writer = nil

	if err != nil {
		return fmt.Errorf("kafka: producer close: %w", err)
	}

	p.logger.Info("kafka producer stopped")

	return nil
}

// Publish sends a message to the given topic. Before sending, the payload is
// passed through all registered PublishHooks in order. W3C trace context is
// injected into the message headers so that consumers can link their spans to
// the producer's trace. Publish implements broker.Publisher.
func (p *Producer) Publish(ctx context.Context, topic string, key string, payload []byte) error {
	ctx, span := p.tracer.Start(ctx, "kafka.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", topic),
			attribute.String("messaging.kafka.message.key", key),
		),
	)
	defer span.End()

	p.mu.RLock()
	w := p.writer
	p.mu.RUnlock()

	if w == nil {
		span.SetStatus(codes.Error, "producer not started")
		return fmt.Errorf("kafka: producer not started")
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
			return fmt.Errorf("kafka: publish hook: %w", err)
		}
	}

	// Inject W3C trace context into message headers.
	headers := InjectTraceContext(ctx, nil)

	msg := kafkago.Message{
		Topic:   topic,
		Key:     []byte(key),
		Value:   transformed,
		Headers: headers,
	}

	if err := w.WriteMessages(ctx, msg); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "write failed")
		return fmt.Errorf("kafka: publish to %q: %w", topic, err)
	}

	span.SetStatus(codes.Ok, "")

	return nil
}
