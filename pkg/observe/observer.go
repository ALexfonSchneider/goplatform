// Package observe provides OpenTelemetry-based observability for the goplatform SDK.
//
// Observer manages trace, metric, and log providers as a platform.Component with
// explicit lifecycle (Start/Stop). Providers are created eagerly in New() so that
// TracerProvider()/MeterProvider() return real (non-noop) providers immediately.
// Start() adds OTLP exporters; Stop() flushes and shuts down.
//
// Usage:
//
//	baseHandler := slog.NewJSONHandler(os.Stdout, nil)
//	obs, _ := observe.New(
//	    observe.WithServiceName("myservice"),
//	    observe.WithOTLPEndpoint("localhost:4317"),
//	    observe.WithBaseHandler(baseHandler),
//	)
//	// TracerProvider() is usable immediately — no need to wait for Start().
//	db, _ := postgres.New(postgres.WithTracerProvider(obs.TracerProvider()))
//	app.Register("observe", obs)
//	logger := obs.Logger()
package observe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	nooplog "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
)

// Observer manages OpenTelemetry traces, metrics, and log export.
// It implements platform.Component with explicit Start/Stop lifecycle.
//
// Providers are created eagerly in New() so that TracerProvider()/MeterProvider()
// return real SDK providers immediately — before Start(). Start() connects
// OTLP exporters; spans/metrics created before Start() are recorded but not
// exported until Start() registers the exporter.
type Observer struct {
	mu sync.Mutex

	serviceName    string
	serviceVersion string
	otlpEndpoint   string

	logger      platform.Logger
	baseHandler slog.Handler
	swap        *swapHandler

	// Providers — created in New() (or injected via options).
	tp       trace.TracerProvider
	mp       metric.MeterProvider
	lp       otellog.LoggerProvider
	customTP bool
	customMP bool
	customLP bool

	// Owned SDK providers — created in New(), shut down in Stop().
	ownedTP *sdktrace.TracerProvider
	ownedMP *sdkmetric.MeterProvider
	ownedLP *log.LoggerProvider

	started bool
}

// Option configures an Observer.
type Option func(*Observer)

// WithServiceName sets the service name used in OTel resource attributes.
func WithServiceName(name string) Option {
	return func(o *Observer) { o.serviceName = name }
}

// WithServiceVersion sets the service version used in OTel resource attributes.
func WithServiceVersion(version string) Option {
	return func(o *Observer) { o.serviceVersion = version }
}

// WithOTLPEndpoint sets the OTLP gRPC endpoint for exporting traces, metrics, and logs.
func WithOTLPEndpoint(endpoint string) Option {
	return func(o *Observer) { o.otlpEndpoint = endpoint }
}

// WithLogger sets the base logger used by the Observer for its own messages.
func WithLogger(l platform.Logger) Option {
	return func(o *Observer) { o.logger = l }
}

// WithBaseHandler sets the base slog.Handler for stdout output. After Start(),
// obs.Logger() writes to both this handler and OTel.
func WithBaseHandler(h slog.Handler) Option {
	return func(o *Observer) { o.baseHandler = h }
}

// WithTracerProvider sets a custom TracerProvider, bypassing internal creation.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(o *Observer) {
		o.tp = tp
		o.customTP = true
	}
}

// WithMeterProvider sets a custom MeterProvider, bypassing internal creation.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *Observer) {
		o.mp = mp
		o.customMP = true
	}
}

// WithLoggerProvider sets a custom LoggerProvider, bypassing internal creation.
func WithLoggerProvider(lp otellog.LoggerProvider) Option {
	return func(o *Observer) {
		o.lp = lp
		o.customLP = true
	}
}

// New creates a new Observer with the given options.
//
// SDK providers are created eagerly (without exporters) so that
// TracerProvider()/MeterProvider() are usable immediately. Start() adds
// OTLP exporters later via RegisterSpanProcessor/AddReader.
func New(opts ...Option) (*Observer, error) {
	o := &Observer{
		serviceName: "unknown",
		logger:      platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(o)
	}

	// Build resource for providers.
	resAttrs := []attribute.KeyValue{semconv.ServiceName(o.serviceName)}
	if o.serviceVersion != "" {
		resAttrs = append(resAttrs, semconv.ServiceVersion(o.serviceVersion))
	}
	res, err := resource.New(context.Background(),
		resource.WithAttributes(resAttrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("observe: build resource: %w", err)
	}

	// Create SDK providers eagerly WITH exporters.
	// OTel gRPC exporters use lazy connections — safe to create in New().
	bgCtx := context.Background()

	if !o.customTP {
		tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
		if o.otlpEndpoint != "" {
			traceExp, err := otlptracegrpc.New(bgCtx,
				otlptracegrpc.WithEndpoint(o.otlpEndpoint),
				otlptracegrpc.WithInsecure(),
			)
			if err != nil {
				return nil, fmt.Errorf("observe: create OTLP trace exporter: %w", err)
			}
			tpOpts = append(tpOpts, sdktrace.WithBatcher(traceExp))
		}
		o.ownedTP = sdktrace.NewTracerProvider(tpOpts...)
		o.tp = o.ownedTP
	}

	if !o.customMP {
		mpOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
		if o.otlpEndpoint != "" {
			metricExp, err := otlpmetricgrpc.New(bgCtx,
				otlpmetricgrpc.WithEndpoint(o.otlpEndpoint),
				otlpmetricgrpc.WithInsecure(),
			)
			if err != nil {
				return nil, fmt.Errorf("observe: create OTLP metric exporter: %w", err)
			}
			mpOpts = append(mpOpts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)))
		}
		o.ownedMP = sdkmetric.NewMeterProvider(mpOpts...)
		o.mp = o.ownedMP
	}

	if !o.customLP {
		lpOpts := []log.LoggerProviderOption{log.WithResource(res)}
		if o.otlpEndpoint != "" {
			logExp, err := otlploggrpc.New(bgCtx,
				otlploggrpc.WithEndpoint(o.otlpEndpoint),
				otlploggrpc.WithInsecure(),
			)
			if err != nil {
				return nil, fmt.Errorf("observe: create OTLP log exporter: %w", err)
			}
			lpOpts = append(lpOpts, log.WithProcessor(log.NewBatchProcessor(logExp)))
		}
		o.ownedLP = log.NewLoggerProvider(lpOpts...)
		o.lp = o.ownedLP
	}

	// Swap handler for combined logging.
	if o.baseHandler != nil {
		o.swap = newSwapHandler(o.baseHandler)
		o.logger = platform.NewSlogLogger(o.swap)
	}

	return o, nil
}

// Start activates the OTel log bridge (swap handler) and marks the observer
// as started. Providers and exporters are already created in New().
func (o *Observer) Start(_ context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.started {
		return fmt.Errorf("observe: already started")
	}

	// Upgrade swap handler: stdout (with trace_id) + OTel.
	if o.swap != nil {
		// Wrap base handler with traceHandler so stdout logs contain trace_id/span_id.
		tracedBase := newTraceHandler(o.baseHandler)

		if o.lp != nil {
			otelHandler := otelslog.NewHandler(o.serviceName,
				otelslog.WithLoggerProvider(o.lp),
			)
			o.swap.swap(newMultiHandler(tracedBase, otelHandler))
		} else {
			o.swap.swap(tracedBase)
		}
	}

	o.started = true
	o.logger.Info("observer started",
		"service", o.serviceName,
		"otlp_endpoint", o.otlpEndpoint,
	)
	return nil
}

// Stop flushes and shuts down all internally created OTel providers.
func (o *Observer) Stop(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.started {
		return nil
	}

	if o.swap != nil && o.baseHandler != nil {
		o.swap.swap(o.baseHandler)
	}

	var errs []error

	if o.ownedTP != nil {
		if err := o.ownedTP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("observe: shutdown tracer provider: %w", err))
		}
	}
	if o.ownedMP != nil {
		if err := o.ownedMP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("observe: shutdown meter provider: %w", err))
		}
	}
	if o.ownedLP != nil {
		if err := o.ownedLP.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("observe: shutdown logger provider: %w", err))
		}
	}

	o.started = false
	o.logger.Info("observer stopped")

	if len(errs) > 0 {
		return fmt.Errorf("observe: stop errors: %v", errs)
	}
	return nil
}

// SetAsGlobal registers the TracerProvider and MeterProvider as OTel globals.
func (o *Observer) SetAsGlobal() {
	if o.tp != nil {
		otel.SetTracerProvider(o.tp)
	}
	if o.mp != nil {
		otel.SetMeterProvider(o.mp)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// TracerProvider returns the configured TracerProvider. Returns a real SDK
// provider immediately after New() — no need to wait for Start().
func (o *Observer) TracerProvider() trace.TracerProvider {
	if o.tp != nil {
		return o.tp
	}
	return nooptrace.NewTracerProvider()
}

// MeterProvider returns the configured MeterProvider.
func (o *Observer) MeterProvider() metric.MeterProvider {
	if o.mp != nil {
		return o.mp
	}
	return noopmetric.NewMeterProvider()
}

// LoggerProvider returns the configured LoggerProvider.
func (o *Observer) LoggerProvider() otellog.LoggerProvider {
	if o.lp != nil {
		return o.lp
	}
	return nooplog.NewLoggerProvider()
}

// Logger returns the structured Logger.
func (o *Observer) Logger() platform.Logger {
	return o.logger
}

// Tracer is a convenience method returning a named Tracer.
func (o *Observer) Tracer(name string) trace.Tracer {
	return o.TracerProvider().Tracer(name)
}

// Meter is a convenience method returning a named Meter.
func (o *Observer) Meter(name string) metric.Meter {
	return o.MeterProvider().Meter(name)
}

// Compile-time check that Observer implements platform.Component.
var _ platform.Component = (*Observer)(nil)
