package platform

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// namedComponent pairs a component with its resolved name.
type namedComponent struct {
	name      string
	component Component
}

// App is the central orchestrator that manages the lifecycle of components and plugins.
// It coordinates startup, shutdown, hooks, and signal handling.
type App struct {
	mu sync.Mutex

	components []namedComponent
	plugins    []Plugin

	beforeStart []BeforeStartHook
	afterStart  []AfterStartHook
	beforeStop  []BeforeStopHook
	afterStop   []AfterStopHook

	logger          Logger
	shutdownTimeout time.Duration
}

// Option configures an App during construction.
type Option func(*App)

// New creates a new App with the provided options applied.
// The default logger is a NopLogger and the default shutdown timeout is 30 seconds.
func New(opts ...Option) *App {
	a := &App{
		logger:          NopLogger(),
		shutdownTimeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// WithLogger returns an Option that sets the App's logger.
func WithLogger(l Logger) Option {
	return func(a *App) {
		a.logger = l
	}
}

// WithShutdownTimeout returns an Option that sets the maximum duration
// the App will wait for components to stop during shutdown.
func WithShutdownTimeout(d time.Duration) Option {
	return func(a *App) {
		a.shutdownTimeout = d
	}
}

// Register adds a named component to the App. If a component with the same
// resolved name already exists, an error is returned.
// If the component implements the Named interface, Name() is used as the
// component's identity instead of the provided name.
func (a *App) Register(name string, c Component) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	resolvedName := name
	if named, ok := c.(Named); ok {
		resolvedName = named.Name()
	}

	for _, nc := range a.components {
		if nc.name == resolvedName {
			return fmt.Errorf("platform: register: component %q already exists", resolvedName)
		}
	}

	a.components = append(a.components, namedComponent{
		name:      resolvedName,
		component: c,
	})
	return nil
}

// Use adds one or more plugins to the App. Plugins are initialized in the
// order they are added and closed in reverse order during shutdown.
func (a *App) Use(plugins ...Plugin) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.plugins = append(a.plugins, plugins...)
}

// OnBeforeStart registers a hook that is called before each component starts.
func (a *App) OnBeforeStart(h BeforeStartHook) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.beforeStart = append(a.beforeStart, h)
}

// OnAfterStart registers a hook that is called after all components have started.
func (a *App) OnAfterStart(h AfterStartHook) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.afterStart = append(a.afterStart, h)
}

// OnBeforeStop registers a hook that is called before components begin stopping.
func (a *App) OnBeforeStop(h BeforeStopHook) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.beforeStop = append(a.beforeStop, h)
}

// OnAfterStop registers a hook that is called after all components have stopped.
func (a *App) OnAfterStop(h AfterStopHook) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.afterStop = append(a.afterStop, h)
}

// Logger returns the App's configured logger.
func (a *App) Logger() Logger {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.logger
}

// Component retrieves a registered component by name.
// Returns the component and true if found, or nil and false otherwise.
func (a *App) Component(name string) (Component, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, nc := range a.components {
		if nc.name == name {
			return nc.component, true
		}
	}
	return nil, false
}

// HealthCheckers returns a map of component names to their HealthChecker
// implementations for all registered components that implement HealthChecker.
func (a *App) HealthCheckers() map[string]HealthChecker {
	a.mu.Lock()
	defer a.mu.Unlock()

	checkers := make(map[string]HealthChecker)
	for _, nc := range a.components {
		if hc, ok := nc.component.(HealthChecker); ok {
			checkers[nc.name] = hc
		}
	}
	return checkers
}

// Run starts all plugins and components, waits for a termination signal
// (SIGINT or SIGTERM) or context cancellation, then gracefully shuts everything down.
//
// Startup order:
//  1. Initialize plugins via Plugin.Init
//  2. Execute BeforeStartHooks — abort on first error
//  3. Start components in registration order — abort on first error, stopping already-started
//  4. Execute AfterStartHooks
//
// Shutdown order (after signal or context cancellation):
//  1. Execute BeforeStopHooks
//  2. Stop components in reverse registration order, collecting all errors
//  3. Execute AfterStopHooks with the collected stop error
//  4. Close plugins in reverse order
//  5. Return all collected errors joined via errors.Join
func (a *App) Run(ctx context.Context) error {
	// Wrap the context with signal handling.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Take a snapshot of state under the lock to avoid holding the lock
	// throughout the entire run.
	a.mu.Lock()
	components := make([]namedComponent, len(a.components))
	copy(components, a.components)
	plugins := make([]Plugin, len(a.plugins))
	copy(plugins, a.plugins)
	beforeStart := make([]BeforeStartHook, len(a.beforeStart))
	copy(beforeStart, a.beforeStart)
	afterStart := make([]AfterStartHook, len(a.afterStart))
	copy(afterStart, a.afterStart)
	beforeStop := make([]BeforeStopHook, len(a.beforeStop))
	copy(beforeStop, a.beforeStop)
	afterStop := make([]AfterStopHook, len(a.afterStop))
	copy(afterStop, a.afterStop)
	logger := a.logger
	shutdownTimeout := a.shutdownTimeout
	a.mu.Unlock()

	// 1. Initialize plugins.
	for _, p := range plugins {
		logger.Info("initializing plugin", "plugin", p.Name())
		if err := p.Init(a); err != nil {
			return fmt.Errorf("platform: plugin init %q: %w", p.Name(), err)
		}
	}

	// Re-snapshot hooks and components since plugins may have registered new ones.
	a.mu.Lock()
	components = make([]namedComponent, len(a.components))
	copy(components, a.components)
	beforeStart = make([]BeforeStartHook, len(a.beforeStart))
	copy(beforeStart, a.beforeStart)
	afterStart = make([]AfterStartHook, len(a.afterStart))
	copy(afterStart, a.afterStart)
	beforeStop = make([]BeforeStopHook, len(a.beforeStop))
	copy(beforeStop, a.beforeStop)
	afterStop = make([]AfterStopHook, len(a.afterStop))
	copy(afterStop, a.afterStop)
	a.mu.Unlock()

	// stopStarted stops all components that have been successfully started,
	// in reverse order, collecting errors.
	stopStarted := func(started []namedComponent) error {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutCancel()

		var errs []error
		for i := len(started) - 1; i >= 0; i-- {
			nc := started[i]
			logger.Info("stopping component", "component", nc.name)
			if err := nc.component.Stop(shutCtx); err != nil {
				errs = append(errs, fmt.Errorf("platform: stop %q: %w", nc.name, err))
			}
		}
		return errors.Join(errs...)
	}

	// 2-3. Start components in registration order, calling BeforeStartHooks before each.
	var started []namedComponent
	for _, nc := range components {
		// Call all BeforeStartHooks for this component.
		for _, h := range beforeStart {
			if err := h(ctx, nc.name); err != nil {
				stopErr := stopStarted(started)
				return errors.Join(
					fmt.Errorf("platform: before start hook %q: %w", nc.name, err),
					stopErr,
				)
			}
		}

		logger.Info("starting component", "component", nc.name)
		if err := nc.component.Start(ctx); err != nil {
			stopErr := stopStarted(started)
			return errors.Join(
				fmt.Errorf("platform: start %q: %w", nc.name, err),
				stopErr,
			)
		}
		started = append(started, nc)
	}

	// 4. Call AfterStartHooks for each started component.
	for _, h := range afterStart {
		for _, nc := range started {
			h(ctx, nc.name)
		}
	}

	logger.Info("all components started, waiting for shutdown signal")

	// 5. Wait for context cancellation (signal or parent cancel).
	<-ctx.Done()
	logger.Info("shutdown signal received")

	// 6. Create shutdown context with timeout.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutCancel()

	// 7. Call BeforeStopHooks.
	for _, h := range beforeStop {
		for _, nc := range components {
			h(shutCtx, nc.name)
		}
	}

	// 8. Stop components in REVERSE order, collecting ALL errors.
	var stopErrs []error
	for i := len(started) - 1; i >= 0; i-- {
		nc := started[i]
		logger.Info("stopping component", "component", nc.name)
		if err := nc.component.Stop(shutCtx); err != nil {
			stopErrs = append(stopErrs, fmt.Errorf("platform: stop %q: %w", nc.name, err))
		}
	}
	stopErr := errors.Join(stopErrs...)

	// 9. Call AfterStopHooks.
	for _, h := range afterStop {
		for _, nc := range components {
			h(shutCtx, nc.name, stopErr)
		}
	}

	// 10. Close plugins in reverse order.
	var closeErrs []error
	for i := len(plugins) - 1; i >= 0; i-- {
		p := plugins[i]
		logger.Info("closing plugin", "plugin", p.Name())
		if err := p.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("platform: plugin close %q: %w", p.Name(), err))
		}
	}

	// 11. Return joined errors.
	return errors.Join(stopErr, errors.Join(closeErrs...))
}
