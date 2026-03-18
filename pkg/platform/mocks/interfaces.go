package mocks

import "github.com/ALexfonSchneider/goplatform/pkg/platform"

// NamedComponent is a composite interface for testing components
// that implement both Component and Named.
type NamedComponent interface {
	platform.Component
	platform.Named
}

// HealthCheckComponent is a composite interface for testing components
// that implement both Component and HealthChecker.
type HealthCheckComponent interface {
	platform.Component
	platform.HealthChecker
}
