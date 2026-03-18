// Package observe provides OpenTelemetry-based observability for the goplatform SDK.
//
// Observer manages trace, metric, and log providers as a platform.Component with
// explicit lifecycle (Start/Stop). By default, no global state is modified —
// providers are created locally and passed explicitly.
//
// Usage:
//
//	baseHandler := slog.NewJSONHandler(os.Stdout, nil)
//	obs, _ := observe.New(
//	    observe.WithServiceName("myservice"),
//	    observe.WithOTLPEndpoint("localhost:4317"),
//	    observe.WithBaseHandler(baseHandler),
//	)
//	app.Register("observe", obs)
//	logger := obs.Logger() // stdout before Start, stdout + OTel after Start
package observe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
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
// When WithBaseHandler is set, Logger() returns a logger that writes to
// stdout before Start() and to both stdout + OTel after Start(). The same
// Logger instance is valid throughout — callers don't need to re-obtain it.
type Observer struct {
	mu sync.Mutex

	serviceName    string
	serviceVersion string
	otlpEndpoint   string

	// Base logger for Observer's own messages (before swap is ready).
	logger platform.Logger

	// Base slog handler (e.g. slog.NewJSONHandler(os.Stdout)).
	// When set, Observer creates a swapHandler-backed Logger.
	baseHandler slog.Handler

	// Swap handler wrapping baseHandler. After Start(), swapped to
	// multiHandler(baseHandler, otelHandler). After Stop(), reverted.
	swap *swapHandler

	// Trace + metric providers.
	tp       trace.TracerProvider
	mp       metric.MeterProvider
	customTP bool
	customMP bool
	ownedTP  *sdktrace.TracerProvider
	ownedMP  *sdkmetric.MeterProvider

	// Log provider.
	lp       otellog.LoggerProvider
	customLP bool
	ownedLP  *log.LoggerProvider

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

// WithOTLPEndpoint sets the OTLP gRPC endpoint for exporting traces, metrics, and logs.
func WithOTLPEndpoint(endpoint string) Option {
	return func(o *Observer) {
		o.otlpEndpoint = endpoint
	}
}

// WithLogger sets the base logger used by the Observer for its own messages.
// For application logging with OTel export, use WithBaseHandler instead.
func WithLogger(l platform.Logger) Option {
	return func(o *Observer) {
		o.logger = l
	}
}

// WithBaseHandler sets the base slog.Handler for stdout output. After Start(),
// obs.Logger() returns a logger that writes to both this handler and OTel.
// Before Start(), it writes to this handler only.
func WithBaseHandler(h slog.Handler) Option {
	return func(o *Observer) {
		o.baseHandler = h
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

// WithLoggerProvider sets a custom LoggerProvider, bypassing internal creation.
// Custom providers are NOT shut down by Stop().
func WithLoggerProvider(lp otellog.LoggerProvider) Option {
	return func(o *Observer) {
		o.lp = lp
		o.customLP = true
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

	// If baseHandler is set, create a swap-backed logger.
	// Before Start: writes to baseHandler only.
	// After Start: swapped to multiHandler(baseHandler, otelHandler).
	if o.baseHandler != nil {
		o.swap = newSwapHandler(o.baseHandler)
		o.logger = platform.NewSlogLogger(o.swap)
	}

	return o, nil
}

// Start initializes OTel exporters and providers.
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

	if !o.customLP {
		lp, err := o.createLoggerProvider(ctx, res)
		if err != nil {
			return fmt.Errorf("observe: create logger provider: %w", err)
		}
		o.ownedLP = lp
		o.lp = lp
	}

	// Upgrade swap handler: stdout + OTel.
	if o.swap != nil && o.lp != nil {
		otelHandler := otelslog.NewHandler(o.serviceName,
			otelslog.WithLoggerProvider(o.lp),
		)
		o.swap.swap(newMultiHandler(o.baseHandler, otelHandler))
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

	// Revert swap to base-only before shutting down LoggerProvider.
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

// LoggerProvider returns the configured LoggerProvider.
func (o *Observer) LoggerProvider() otellog.LoggerProvider {
	if o.lp != nil {
		return o.lp
	}
	return nooplog.NewLoggerProvider()
}

// Logger returns the structured Logger. When WithBaseHandler is set, the
// same Logger instance writes to stdout before Start() and to stdout + OTel
// after Start(). After Stop(), it reverts to stdout only.
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

func (o *Observer) createLoggerProvider(ctx context.Context, res *resource.Resource) (*log.LoggerProvider, error) {
	var opts []log.LoggerProviderOption
	opts = append(opts, log.WithResource(res))

	if o.otlpEndpoint != "" {
		exporter, err := otlploggrpc.New(ctx,
			otlploggrpc.WithEndpoint(o.otlpEndpoint),
			otlploggrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("observe: create OTLP log exporter: %w", err)
		}
		opts = append(opts, log.WithProcessor(log.NewBatchProcessor(exporter)))
	}

	return log.NewLoggerProvider(opts...), nil
}

// Compile-time check that Observer implements platform.Component.
var _ platform.Component = (*Observer)(nil)
