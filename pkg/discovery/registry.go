// Package discovery defines interfaces and types for service registration,
// deregistration, and discovery. It provides a transport-agnostic contract
// that can be backed by Consul, etcd, or any other service registry.
//
// The key abstractions are:
//   - Instance: represents a single service instance with address, port, tags,
//     and metadata.
//   - Registry: provides service registration, deregistration, and discovery
//     with optional health-aware watching.
//   - Resolver: client-side load balancer with TTL-based caching and
//     round-robin instance selection.
package discovery

import "context"

// Instance represents a service instance registered with the service registry.
// Each instance is uniquely identified by its ID and belongs to a named service.
// Tags and Meta allow attaching arbitrary labels and key-value metadata to the
// instance for filtering and routing purposes.
type Instance struct {
	// ID is the unique identifier for this service instance.
	ID string

	// Name is the logical service name (e.g., "user-service").
	Name string

	// Address is the network address (hostname or IP) where the instance
	// can be reached.
	Address string

	// Port is the port number the instance listens on.
	Port int

	// Tags is an optional list of string labels associated with the instance.
	// Tags can be used for filtering or version-based routing.
	Tags []string

	// Meta is an optional set of key-value metadata associated with the instance.
	Meta map[string]string
}

// Registry provides service registration, deregistration, and discovery.
// Implementations must be safe for concurrent use.
type Registry interface {
	// Register registers a service instance with the registry. The instance's
	// ID must be unique; re-registering an existing ID updates the entry.
	Register(ctx context.Context, inst Instance) error

	// Deregister removes a service instance from the registry by its unique ID.
	Deregister(ctx context.Context, id string) error

	// Discover returns all healthy instances of the named service. If no
	// instances are found, it returns an empty slice and no error.
	Discover(ctx context.Context, name string) ([]Instance, error)

	// Watch returns a channel that emits updated instance lists whenever the
	// service's health status changes. The channel is closed when ctx is
	// cancelled. Implementations should use efficient mechanisms such as
	// blocking queries or long-polling rather than fixed-interval polling.
	Watch(ctx context.Context, name string) (<-chan []Instance, error)
}
