// Package observe provides OpenTelemetry-based observability for the goplatform SDK.
//
// Observer manages trace and metric providers as a platform.Component with explicit
// lifecycle (Start/Stop). By default, no global state is modified — providers are
// created locally and passed explicitly. Call SetAsGlobal() only if you need
// otel.GetTracerProvider() / otel.GetMeterProvider() to work.
//
// Usage:
//
//	obs, _ := observe.New(
//	    observe.WithServiceName("myservice"),
//	    observe.WithOTLPEndpoint("localhost:4317"),
//	)
//	app.Register("observe", obs)
//
//	// Pass providers to other packages explicitly:
//	srv := server.New(server.WithTracerProvider(obs.TracerProvider()))
//
//	// Or create custom metrics:
//	counter, _ := obs.Meter("myservice").Int64Counter("orders_created")
//	counter.Add(ctx, 1)
package observe

import (
	"context"
	"fmt"
	"sync"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

// Observer manages OpenTelemetry traces, metrics, and structured logging.
// It implements platform.Component with explicit Start/Stop lifecycle.
//
// Two Observers can coexist in the same process without interference —
// each creates its own TracerProvider and MeterProvider.
//
// Custom providers can be injected via WithTracerProvider / WithMeterProvider
// for testing or advanced setups. Injected providers are NOT shut down by Stop.
type Observer struct {
	mu sync.Mutex

	// Service identity for OTel resource attributes.
	serviceName    string
	serviceVersion string

	// OTLP gRPC endpoint (e.g. "localhost:4317"). Empty = no exporter.
	otlpEndpoint string

	// Base logger for Observer's own messages.
	logger platform.Logger

	// Active providers. May be custom-injected or internally created.
	tp trace.TracerProvider
	mp metric.MeterProvider

	// Flags indicating whether providers were injected externally.
	// External providers are not shut down by Stop().
	customTP bool
	customMP bool

	// Internally created providers that we own and must shut down.
	// nil if custom providers were injected.
	ownedTP *sdktrace.TracerProvider
	ownedMP *sdkmetric.MeterProvider

	// Guards against double Start.
	started bool
}

// Option configures an Observer.
type Option func(*Observer)

// WithServiceName sets the service name used in OTel resource attributes.
func WithServiceName(name string) Option {
	return func(o *Observer) {
		o.serviceName = name
	}
}

// WithServiceVersion sets the service version used in OTel resource attributes.
func WithServiceVersion(version string) Option {
	return func(o *Observer) {
		o.serviceVersion = version
	}
}

// WithOTLPEndpoint sets the OTLP gRPC endpoint for exporting traces and metrics.
func WithOTLPEndpoint(endpoint string) Option {
	return func(o *Observer) {
		o.otlpEndpoint = endpoint
	}
}

// WithLogger sets the base logger used by the Observer.
func WithLogger(l platform.Logger) Option {
	return func(o *Observer) {
		o.logger = l
	}
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

// New creates a new Observer with the given options.
func New(opts ...Option) (*Observer, error) {
	o := &Observer{
		serviceName: "unknown",
		logger:      platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o, nil
}

// Start initializes OTel exporters and providers. Implements platform.Component.
//
// If OTLP endpoint is set, creates OTLP gRPC exporters for traces and metrics.
// Otherwise, creates SDK providers without exporters (useful for tests and
// local development where spans are still created but not exported).
//
// Custom providers injected via WithTracerProvider/WithMeterProvider are used
// as-is — no exporters are created for them.
func (o *Observer) Start(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.started {
		return fmt.Errorf("observe: already started")
	}

	res, err := o.buildResource(ctx)
	if err != nil {
		return fmt.Errorf("observe: build resource: %w", err)
	}

	if !o.customTP {
		tp, err := o.createTracerProvider(ctx, res)
		if err != nil {
			return fmt.Errorf("observe: create tracer provider: %w", err)
		}
		o.ownedTP = tp
		o.tp = tp
	}

	if !o.customMP {
		mp, err := o.createMeterProvider(ctx, res)
		if err != nil {
			return fmt.Errorf("observe: create meter provider: %w", err)
		}
		o.ownedMP = mp
		o.mp = mp
	}

	o.started = true
	o.logger.Info("observer started",
		"service", o.serviceName,
		"otlp_endpoint", o.otlpEndpoint,
	)
	return nil
}

// Stop flushes and shuts down all internally created OTel providers.
// Custom providers (injected via WithTracerProvider/WithMeterProvider) are NOT
// shut down — their lifecycle is managed by the caller.
// Respects ctx deadline; returns error if shutdown exceeds the timeout.
func (o *Observer) Stop(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.started {
		return nil
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

	o.started = false
	o.logger.Info("observer stopped")

	if len(errs) > 0 {
		return fmt.Errorf("observe: stop errors: %v", errs)
	}
	return nil
}

// SetAsGlobal registers the TracerProvider and MeterProvider as OTel globals.
// This is opt-in — by default Observer does NOT modify global state.
// Call this only if third-party libraries require otel.GetTracerProvider().
// Also sets the W3C TraceContext + Baggage propagator as the global propagator.
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

// TracerProvider returns the configured TracerProvider.
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

// Logger returns a structured Logger. Trace correlation in logs is handled
// at the slog handler level; this returns the base logger as-is.
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

// buildResource creates an OTel Resource with service.name and optionally
// service.version attributes. The resource identifies the service in traces/metrics.
func (o *Observer) buildResource(ctx context.Context) (*resource.Resource, error) {
	attrs := []resource.Option{
		resource.WithAttributes(
			semconv.ServiceName(o.serviceName),
		),
	}
	if o.serviceVersion != "" {
		attrs = append(attrs, resource.WithAttributes(
			semconv.ServiceVersion(o.serviceVersion),
		))
	}
	return resource.New(ctx, attrs...)
}

// createTracerProvider builds an SDK TracerProvider. If OTLP endpoint is set,
// attaches a BatchSpanProcessor with the OTLP gRPC exporter. Otherwise creates
// a provider without exporters (spans are created but not exported).
func (o *Observer) createTracerProvider(ctx context.Context, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	var opts []sdktrace.TracerProviderOption
	opts = append(opts, sdktrace.WithResource(res))

	if o.otlpEndpoint != "" {
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(o.otlpEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("observe: create OTLP trace exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}

	return sdktrace.NewTracerProvider(opts...), nil
}

// createMeterProvider builds an SDK MeterProvider. If OTLP endpoint is set,
// attaches a PeriodicReader with the OTLP gRPC exporter. Otherwise creates
// a provider without readers (metrics are recorded but not exported).
func (o *Observer) createMeterProvider(ctx context.Context, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	var opts []sdkmetric.Option
	opts = append(opts, sdkmetric.WithResource(res))

	if o.otlpEndpoint != "" {
		exporter, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(o.otlpEndpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("observe: create OTLP metric exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)))
	}

	return sdkmetric.NewMeterProvider(opts...), nil
}

// Compile-time check that Observer implements platform.Component.
var _ platform.Component = (*Observer)(nil)
