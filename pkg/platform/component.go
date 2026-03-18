package platform

import "context"

// Component represents a managed lifecycle component.
// Implementations must be safe to call Start and Stop from different goroutines.
type Component interface {
	// Start initializes and starts the component. The provided context
	// carries cancellation signals from the parent application.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the component. The provided context
	// carries a deadline derived from the application's shutdown timeout.
	Stop(ctx context.Context) error
}

// Named is an optional interface a Component can implement to declare its name.
// When a component implements Named, the App uses Name() as the component's
// identity instead of the name provided at registration time.
type Named interface {
	// Name returns the component's declared name.
	Name() string
}

// HealthChecker is an optional interface a Component can implement to provide health checks.
// Components that implement HealthChecker are automatically discovered by App.HealthCheckers.
type HealthChecker interface {
	// HealthCheck performs a health check and returns an error if the component is unhealthy.
	HealthCheck(ctx context.Context) error
}
