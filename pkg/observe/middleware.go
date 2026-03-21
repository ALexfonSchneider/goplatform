package observe

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// HTTPMiddleware returns an HTTP middleware that creates spans for incoming requests
// and records basic metrics (request count, duration).
func HTTPMiddleware(tp trace.TracerProvider, mp metric.MeterProvider) func(http.Handler) http.Handler {
	tracer := tp.Tracer("observe.http")
	meter := mp.Meter("observe.http")

	requestCount, _ := meter.Int64Counter("http.server.request_count",
		metric.WithDescription("Total number of HTTP requests"),
	)
	requestDuration, _ := meter.Float64Histogram("http.server.duration",
		metric.WithDescription("HTTP request duration in milliseconds"),
		metric.WithUnit("ms"),
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spanName := r.Method + " " + r.URL.Path
			ctx, span := tracer.Start(r.Context(), spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("url.full", r.URL.String()),
				),
			)
			defer span.End()

			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rec, r.WithContext(ctx))

			duration := float64(time.Since(start).Milliseconds())

			span.SetAttributes(attribute.Int("http.status_code", rec.statusCode))

			attrs := metric.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.Int("http.status_code", rec.statusCode),
			)
			requestCount.Add(ctx, 1, attrs)
			requestDuration.Record(ctx, duration, attrs)
		})
	}
}
