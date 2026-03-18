package kafka

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	_ broker.Subscriber  = (*Consumer)(nil)
	_ platform.Component = (*Consumer)(nil)
)

// Consumer implements broker.Subscriber and platform.Component. It wraps
// kafka-go's Reader for consuming messages from Kafka topics. Each call to
// Subscribe creates a dedicated Reader and goroutine for the given topic.
type Consumer struct {
	mu      sync.Mutex
	readers []*kafkago.Reader
	cancels []context.CancelFunc
	wg      sync.WaitGroup

	brokers        []string
	groupID        string
	middlewares    []broker.Middleware
	dlqPublisher   broker.Publisher
	dlqTopicSuffix string
	maxRetries     int
	retryBackoff   time.Duration
	logger         platform.Logger
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer

	// kafka-go Reader tuning.
	minBytes               int
	maxBytes               int
	maxWait                time.Duration
	commitInterval         time.Duration
	startOffset            int64
	heartbeatInterval      time.Duration
	sessionTimeout         time.Duration
	rebalanceTimeout       time.Duration
	partitionWatchInterval time.Duration
}

// ConsumerOption configures a Consumer.
type ConsumerOption func(*Consumer)

// WithConsumerBrokers sets the Kafka broker addresses for the consumer. At
// least one address must be provided before Subscribe is called.
func WithConsumerBrokers(addrs ...string) ConsumerOption {
	return func(c *Consumer) {
		c.brokers = addrs
	}
}

// WithGroupID sets the consumer group ID. When a group ID is set, Kafka manages
// partition assignment and offset tracking across all consumers in the group.
func WithGroupID(id string) ConsumerOption {
	return func(c *Consumer) {
		c.groupID = id
	}
}

// WithMiddleware appends one or more middleware to the consumer's middleware
// chain. Middleware are applied to the handler in registration order (outermost
// first), following the same composition semantics as net/http middleware.
func WithMiddleware(mw ...broker.Middleware) ConsumerOption {
	return func(c *Consumer) {
		c.middlewares = append(c.middlewares, mw...)
	}
}

// WithDLQ configures dead-letter queue routing. When a message exhausts all
// retries, it is published to a topic named "<original-topic><topicSuffix>"
// using the provided publisher. If the DLQ publish fails, the message offset is
// not committed to prevent data loss.
func WithDLQ(publisher broker.Publisher, topicSuffix string) ConsumerOption {
	return func(c *Consumer) {
		c.dlqPublisher = publisher
		c.dlqTopicSuffix = topicSuffix
	}
}

// WithRetry configures the retry policy for failed message handlers. Messages
// that fail processing are retried up to maxRetries times with the given
// backoff duration between attempts. If maxRetries is zero, no retries are
// attempted.
func WithRetry(maxRetries int, backoff time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.maxRetries = maxRetries
		c.retryBackoff = backoff
	}
}

// WithConsumerLogger sets the structured logger for the consumer. If not set,
// a no-op logger is used.
func WithConsumerLogger(l platform.Logger) ConsumerOption {
	return func(c *Consumer) {
		c.logger = l
	}
}

// WithConsumerTracerProvider sets the OpenTelemetry TracerProvider used by the
// consumer to create spans for message processing. If not set, the global
// TracerProvider is used.
func WithConsumerTracerProvider(tp trace.TracerProvider) ConsumerOption {
	return func(c *Consumer) {
		c.tracerProvider = tp
	}
}

// WithMinBytes sets the minimum number of bytes to fetch per request.
// kafka-go default: 1.
func WithMinBytes(n int) ConsumerOption {
	return func(c *Consumer) {
		c.minBytes = n
	}
}

// WithMaxBytes sets the maximum number of bytes to fetch per request.
// kafka-go default: 1MB.
func WithMaxBytes(n int) ConsumerOption {
	return func(c *Consumer) {
		c.maxBytes = n
	}
}

// WithMaxWait sets the maximum time the broker will wait for MinBytes to be
// available before returning a fetch response. kafka-go default: 10s.
func WithMaxWait(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.maxWait = d
	}
}

// WithCommitInterval sets the interval for automatic offset commits.
// Zero disables auto-commit (offsets are committed synchronously after each
// message). kafka-go default: 0 (synchronous commit).
func WithCommitInterval(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.commitInterval = d
	}
}

// WithStartOffset sets the offset to start reading from when no committed
// offset exists. Use kafkago.FirstOffset (-2) to start from the beginning,
// or kafkago.LastOffset (-1) to start from the latest. kafka-go default: FirstOffset.
func WithStartOffset(offset int64) ConsumerOption {
	return func(c *Consumer) {
		c.startOffset = offset
	}
}

// WithHeartbeatInterval sets the interval between heartbeats sent to the
// consumer group coordinator. Must be less than SessionTimeout.
// kafka-go default: 3s.
func WithHeartbeatInterval(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.heartbeatInterval = d
	}
}

// WithSessionTimeout sets the timeout for the consumer group session.
// If no heartbeat is received within this period, the consumer is removed
// from the group and a rebalance is triggered. kafka-go default: 30s.
func WithSessionTimeout(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.sessionTimeout = d
	}
}

// WithRebalanceTimeout sets the maximum time a rebalance operation can take.
// kafka-go default: 30s.
func WithRebalanceTimeout(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.rebalanceTimeout = d
	}
}

// WithPartitionWatchInterval sets the interval for checking for new partitions.
// Zero disables partition watching. kafka-go default: 5s.
func WithPartitionWatchInterval(d time.Duration) ConsumerOption {
	return func(c *Consumer) {
		c.partitionWatchInterval = d
	}
}

// NewConsumer creates a new Consumer with the given options. The consumer does
// not start reading until Subscribe is called. Returns an error if the
// configuration is invalid.
func NewConsumer(opts ...ConsumerOption) (*Consumer, error) {
	c := &Consumer{
		logger: platform.NopLogger(),
	}

	for _, opt := range opts {
		opt(c)
	}

	if len(c.brokers) == 0 {
		return nil, fmt.Errorf("kafka: consumer requires at least one broker address")
	}

	if c.tracerProvider == nil {
		c.tracerProvider = otel.GetTracerProvider()
	}
	c.tracer = c.tracerProvider.Tracer("github.com/ALexfonSchneider/goplatform/pkg/broker/kafka")

	return c, nil
}

// Start is a no-op for Consumer because actual reading begins when Subscribe is
// called. Start implements platform.Component.
func (c *Consumer) Start(_ context.Context) error {
	c.logger.Info("kafka consumer started", "brokers", c.brokers, "group_id", c.groupID)
	return nil
}

// Stop gracefully shuts down all active readers. In-flight handlers are allowed
// to complete within the context deadline. Stop implements platform.Component.
func (c *Consumer) Stop(_ context.Context) error {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, len(c.cancels))
	copy(cancels, c.cancels)
	readers := make([]*kafkago.Reader, len(c.readers))
	copy(readers, c.readers)
	c.mu.Unlock()

	// Signal all consume loops to stop.
	for _, cancel := range cancels {
		cancel()
	}

	// Close all readers so that FetchMessage unblocks.
	var firstErr error
	for _, r := range readers {
		if err := r.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Wait for all in-flight handlers to complete.
	c.wg.Wait()

	c.logger.Info("kafka consumer stopped")

	if firstErr != nil {
		return fmt.Errorf("kafka: consumer close: %w", firstErr)
	}

	return nil
}

// Subscribe starts consuming messages from the given topic in a background
// goroutine, calling handler for each message. The handler is wrapped with any
// registered middleware before invocation. Subscribe implements
// broker.Subscriber.
func (c *Consumer) Subscribe(ctx context.Context, topic string, handler broker.Handler) error {
	cfg := kafkago.ReaderConfig{
		Brokers: c.brokers,
		Topic:   topic,
		GroupID: c.groupID,
	}
	if c.minBytes > 0 {
		cfg.MinBytes = c.minBytes
	}
	if c.maxBytes > 0 {
		cfg.MaxBytes = c.maxBytes
	}
	if c.maxWait > 0 {
		cfg.MaxWait = c.maxWait
	}
	if c.commitInterval > 0 {
		cfg.CommitInterval = c.commitInterval
	}
	if c.startOffset != 0 {
		cfg.StartOffset = c.startOffset
	}
	if c.heartbeatInterval > 0 {
		cfg.HeartbeatInterval = c.heartbeatInterval
	}
	if c.sessionTimeout > 0 {
		cfg.SessionTimeout = c.sessionTimeout
	}
	if c.rebalanceTimeout > 0 {
		cfg.RebalanceTimeout = c.rebalanceTimeout
	}
	if c.partitionWatchInterval > 0 {
		cfg.PartitionWatchInterval = c.partitionWatchInterval
	}
	reader := kafkago.NewReader(cfg)

	loopCtx, cancel := context.WithCancel(ctx)

	c.mu.Lock()
	c.readers = append(c.readers, reader)
	c.cancels = append(c.cancels, cancel)
	c.mu.Unlock()

	// Apply middleware chain to the handler.
	wrapped := applyMiddleware(handler, c.middlewares)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.consumeLoop(loopCtx, reader, topic, wrapped)
	}()

	c.logger.Info("kafka subscription started", "topic", topic, "group_id", c.groupID)

	return nil
}

// consumeLoop continuously fetches messages from the reader and processes them
// until the context is cancelled or the reader is closed.
func (c *Consumer) consumeLoop(ctx context.Context, reader *kafkago.Reader, topic string, handler broker.Handler) {
	for {
		fetchMsg, err := reader.FetchMessage(ctx)
		if err != nil {
			// Context cancelled or reader closed — normal shutdown path.
			if ctx.Err() != nil {
				return
			}
			c.logger.Error("kafka: fetch message failed", "topic", topic, "error", err)
			return
		}

		c.processMessage(ctx, reader, topic, fetchMsg, handler)
	}
}

// processMessage handles a single fetched message, including tracing, retry
// logic, and DLQ routing.
func (c *Consumer) processMessage(
	ctx context.Context,
	reader *kafkago.Reader,
	topic string,
	fetchMsg kafkago.Message,
	handler broker.Handler,
) {
	// Extract W3C trace context from message headers.
	msgCtx := ExtractTraceContext(ctx, fetchMsg.Headers)

	msgCtx, span := c.tracer.Start(msgCtx, "kafka.consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", topic),
			attribute.Int("messaging.kafka.partition", fetchMsg.Partition),
			attribute.Int64("messaging.kafka.offset", fetchMsg.Offset),
		),
	)
	defer span.End()

	// Build the broker.Message from the kafka-go message.
	msg := broker.Message{
		Key:       fetchMsg.Key,
		Value:     fetchMsg.Value,
		Headers:   kafkaHeadersToMap(fetchMsg.Headers),
		Topic:     topic,
		Partition: fetchMsg.Partition,
		Offset:    fetchMsg.Offset,
	}

	// Attempt handler invocation with retry logic.
	var handlerErr error
	attempts := 1 + c.maxRetries // 1 initial attempt + retries
	for i := range attempts {
		handlerErr = handler(msgCtx, msg)
		if handlerErr == nil {
			break
		}

		c.logger.Warn("kafka: handler failed",
			"topic", topic,
			"partition", fetchMsg.Partition,
			"offset", fetchMsg.Offset,
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
		case <-msgCtx.Done():
			span.RecordError(msgCtx.Err())
			span.SetStatus(codes.Error, "context cancelled during retry")
			return
		case <-time.After(c.retryBackoff):
		}
	}

	if handlerErr != nil {
		span.RecordError(handlerErr)

		// Route to DLQ if configured.
		if c.dlqPublisher != nil {
			dlqTopic := topic + c.dlqTopicSuffix
			dlqErr := c.dlqPublisher.Publish(msgCtx, dlqTopic, string(fetchMsg.Key), fetchMsg.Value)
			if dlqErr != nil {
				// DLQ publish failed — do NOT commit offset to prevent data loss.
				span.RecordError(dlqErr)
				span.SetStatus(codes.Error, "dlq publish failed")
				c.logger.Error("kafka: dlq publish failed, offset not committed",
					"topic", topic,
					"dlq_topic", dlqTopic,
					"partition", fetchMsg.Partition,
					"offset", fetchMsg.Offset,
					"handler_error", handlerErr,
					"dlq_error", dlqErr,
				)
				return
			}

			c.logger.Info("kafka: message routed to dlq",
				"topic", topic,
				"dlq_topic", dlqTopic,
				"partition", fetchMsg.Partition,
				"offset", fetchMsg.Offset,
			)
		} else {
			// No DLQ configured — commit offset and log the error to avoid blocking.
			span.SetStatus(codes.Error, "handler failed, no dlq configured")
			c.logger.Error("kafka: handler failed, no dlq configured, committing offset",
				"topic", topic,
				"partition", fetchMsg.Partition,
				"offset", fetchMsg.Offset,
				"error", handlerErr,
			)
		}
	} else {
		span.SetStatus(codes.Ok, "")
	}

	// Commit the offset.
	if err := reader.CommitMessages(msgCtx, fetchMsg); err != nil {
		c.logger.Error("kafka: commit offset failed",
			"topic", topic,
			"partition", fetchMsg.Partition,
			"offset", fetchMsg.Offset,
			"error", err,
		)
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

// kafkaHeadersToMap converts a slice of kafka.Header to a map[string]string.
func kafkaHeadersToMap(headers []kafkago.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	m := make(map[string]string, len(headers))
	for _, h := range headers {
		m[h.Key] = string(h.Value)
	}
	return m
}
