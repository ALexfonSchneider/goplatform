// Package kafka provides a Kafka-backed implementation of the broker.Publisher
// and broker.Subscriber interfaces using github.com/segmentio/kafka-go. It
// supports W3C trace context propagation, publish hooks, consumer middleware,
// dead-letter queues, and configurable retry policies.
package kafka

import (
	"context"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/propagation"
)

// kafkaHeaderCarrier adapts a slice of kafka.Header to the
// propagation.TextMapCarrier interface so that OpenTelemetry propagators can
// inject and extract W3C trace context directly into/from Kafka message headers.
type kafkaHeaderCarrier []kafka.Header

// Get returns the value associated with the passed key. If no header with that
// key exists, an empty string is returned.
func (c *kafkaHeaderCarrier) Get(key string) string {
	for _, h := range *c {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set stores the key-value pair in the carrier. If a header with the same key
// already exists it is overwritten; otherwise a new header is appended.
func (c *kafkaHeaderCarrier) Set(key, value string) {
	for i, h := range *c {
		if h.Key == key {
			(*c)[i].Value = []byte(value)
			return
		}
	}
	*c = append(*c, kafka.Header{Key: key, Value: []byte(value)})
}

// Keys returns a slice of all header keys present in the carrier.
func (c *kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(*c))
	for _, h := range *c {
		keys = append(keys, h.Key)
	}
	return keys
}

// InjectTraceContext injects the current span's W3C trace context from ctx into
// the provided Kafka headers. The returned slice may be longer than the input
// when new propagation headers are added.
func InjectTraceContext(ctx context.Context, headers []kafka.Header) []kafka.Header {
	carrier := kafkaHeaderCarrier(headers)
	propagation.TraceContext{}.Inject(ctx, &carrier)
	return carrier
}

// ExtractTraceContext extracts W3C trace context from Kafka headers and returns
// a new context that carries the remote span context. Downstream spans created
// from this context will be linked to the producer's trace.
func ExtractTraceContext(ctx context.Context, headers []kafka.Header) context.Context {
	carrier := kafkaHeaderCarrier(headers)
	return propagation.TraceContext{}.Extract(ctx, &carrier)
}
