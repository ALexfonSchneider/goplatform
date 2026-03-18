// Package nats provides a NATS-backed implementation of the broker.Publisher
// and broker.Subscriber interfaces using github.com/nats-io/nats.go. It
// supports both core NATS (fire-and-forget) and JetStream (persistent) modes,
// W3C trace context propagation, publish hooks, consumer middleware,
// dead-letter queues, and configurable retry policies.
package nats

import (
	"context"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
)

// natsHeaderCarrier adapts natsgo.Header (which is http.Header under the hood,
// i.e. map[string][]string) to the propagation.TextMapCarrier interface so that
// OpenTelemetry propagators can inject and extract W3C trace context directly
// into/from NATS message headers.
type natsHeaderCarrier natsgo.Header

// Get returns the first value associated with the passed key. If no header with
// that key exists, an empty string is returned.
func (c natsHeaderCarrier) Get(key string) string {
	return natsgo.Header(c).Get(key)
}

// Set stores the key-value pair in the carrier, replacing any existing values
// associated with the key.
func (c natsHeaderCarrier) Set(key, value string) {
	natsgo.Header(c).Set(key, value)
}

// Keys returns a slice of all header keys present in the carrier.
func (c natsHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceContext injects the current span's W3C trace context from ctx into
// the provided NATS headers. If header is nil, a new natsgo.Header is created.
// The returned header contains the propagation entries (e.g. traceparent,
// tracestate).
func InjectTraceContext(ctx context.Context, header natsgo.Header) natsgo.Header {
	if header == nil {
		header = make(natsgo.Header)
	}
	carrier := natsHeaderCarrier(header)
	propagation.TraceContext{}.Inject(ctx, carrier)
	return header
}

// ExtractTraceContext extracts W3C trace context from NATS headers and returns
// a new context that carries the remote span context. Downstream spans created
// from this context will be linked to the producer's trace.
func ExtractTraceContext(ctx context.Context, header natsgo.Header) context.Context {
	if header == nil {
		return ctx
	}
	carrier := natsHeaderCarrier(header)
	return propagation.TraceContext{}.Extract(ctx, carrier)
}
