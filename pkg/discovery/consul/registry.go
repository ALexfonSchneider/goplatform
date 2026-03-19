// Package consul provides a Consul-backed implementation of discovery.Registry.
// It uses the official HashiCorp Consul API client for service registration,
// deregistration, health-aware discovery, and blocking-query-based watching.
package consul

import (
	"context"
	"fmt"
	"sync"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/ALexfonSchneider/goplatform/pkg/discovery"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time check that Registry implements discovery.Registry.
var _ discovery.Registry = (*Registry)(nil)

// Registry is a Consul-backed implementation of discovery.Registry.
// It wraps the official HashiCorp Consul API client and provides service
// registration with optional TTL-based health checks, deregistration,
// health-aware discovery, and efficient blocking-query-based watching.
//
// Registry is safe for concurrent use.
type Registry struct {
	client *consulapi.Client
	logger platform.Logger
}

// Option configures a Registry.
type Option func(*config)

// config holds the configuration values applied via Option functions.
type config struct {
	address string
	token   string
	logger  platform.Logger
}

// WithAddress sets the Consul HTTP API address (e.g., "localhost:8500").
// The default is "localhost:8500".
func WithAddress(addr string) Option {
	return func(c *config) {
		c.address = addr
	}
}

// WithToken sets the Consul ACL token used for authentication.
// If empty, no token is sent with requests.
func WithToken(token string) Option {
	return func(c *config) {
		c.token = token
	}
}

// WithLogger sets the structured logger for the Consul registry.
// If not set, a no-op logger is used.
func WithLogger(l platform.Logger) Option {
	return func(c *config) {
		c.logger = l
	}
}

// New creates a new Consul-backed Registry with the given options.
// It initialises the underlying Consul API client using the provided address
// and optional ACL token. Returns an error if the client cannot be created.
func New(opts ...Option) (*Registry, error) {
	cfg := &config{
		address: "localhost:8500",
		logger:  platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	consulCfg := consulapi.DefaultConfig()
	consulCfg.Address = cfg.address

	if cfg.token != "" {
		consulCfg.Token = cfg.token
	}

	client, err := consulapi.NewClient(consulCfg)
	if err != nil {
		return nil, fmt.Errorf("consul: create client: %w", err)
	}

	return &Registry{
		client: client,
		logger: cfg.logger,
	}, nil
}

// Register registers a service instance with Consul, including an optional
// TTL-based health check. The check TTL is set to 30 seconds; callers should
// periodically update the check status to keep the service healthy.
func (r *Registry) Register(ctx context.Context, inst discovery.Instance) error {
	reg := &consulapi.AgentServiceRegistration{
		ID:      inst.ID,
		Name:    inst.Name,
		Address: inst.Address,
		Port:    inst.Port,
		Tags:    inst.Tags,
		Meta:    inst.Meta,
		Check: &consulapi.AgentServiceCheck{
			TTL:                            "30s",
			DeregisterCriticalServiceAfter: "90s",
		},
	}

	if err := r.client.Agent().ServiceRegisterOpts(reg, consulapi.ServiceRegisterOpts{}.WithContext(ctx)); err != nil {
		return fmt.Errorf("consul: register service %q: %w", inst.ID, err)
	}

	// Mark the check as passing immediately so the service is discoverable.
	checkID := "service:" + inst.ID
	if err := r.client.Agent().UpdateTTL(checkID, "initial registration", consulapi.HealthPassing); err != nil {
		return fmt.Errorf("consul: update TTL for %q: %w", inst.ID, err)
	}

	r.logger.Info("consul: registered service",
		"id", inst.ID,
		"name", inst.Name,
		"address", inst.Address,
		"port", inst.Port,
	)

	return nil
}

// Deregister removes a service instance from Consul by its unique ID.
func (r *Registry) Deregister(ctx context.Context, id string) error {
	opts := &consulapi.QueryOptions{}
	opts = opts.WithContext(ctx)

	if err := r.client.Agent().ServiceDeregisterOpts(id, opts); err != nil {
		return fmt.Errorf("consul: deregister service %q: %w", id, err)
	}

	r.logger.Info("consul: deregistered service", "id", id)
	return nil
}

// Discover returns all healthy instances of the named service. It queries
// Consul's Health API with the passing filter enabled, so only instances
// with all checks passing are returned.
func (r *Registry) Discover(ctx context.Context, name string) ([]discovery.Instance, error) {
	opts := &consulapi.QueryOptions{}
	opts = opts.WithContext(ctx)

	entries, _, err := r.client.Health().Service(name, "", true, opts)
	if err != nil {
		return nil, fmt.Errorf("consul: discover service %q: %w", name, err)
	}

	instances := make([]discovery.Instance, 0, len(entries))
	for _, entry := range entries {
		instances = append(instances, toInstance(entry))
	}

	return instances, nil
}

// Watch returns a channel that emits updated instance lists whenever the
// service's health status changes. It uses Consul's blocking query mechanism
// (WaitIndex) for efficient change detection rather than fixed-interval
// polling. The channel is closed when ctx is cancelled.
func (r *Registry) Watch(ctx context.Context, name string) (<-chan []discovery.Instance, error) {
	ch := make(chan []discovery.Instance)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer close(ch)
		defer wg.Done()

		var waitIndex uint64

		for {
			opts := &consulapi.QueryOptions{
				WaitIndex: waitIndex,
			}
			opts = opts.WithContext(ctx)

			entries, meta, err := r.client.Health().Service(name, "", true, opts)
			if err != nil {
				// If the context was cancelled, exit gracefully.
				if ctx.Err() != nil {
					return
				}
				r.logger.Error("consul: watch error, retrying",
					"service", name,
					"error", err,
				)
				// Retry with backoff to avoid tight error loops.
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}

			instances := make([]discovery.Instance, 0, len(entries))
			for _, entry := range entries {
				instances = append(instances, toInstance(entry))
			}

			// Only send an update if the index actually changed.
			if meta.LastIndex != waitIndex {
				waitIndex = meta.LastIndex
				select {
				case <-ctx.Done():
					return
				case ch <- instances:
				}
			}
		}
	}()

	return ch, nil
}

// toInstance converts a Consul ServiceEntry into a discovery.Instance.
func toInstance(entry *consulapi.ServiceEntry) discovery.Instance {
	svc := entry.Service
	return discovery.Instance{
		ID:      svc.ID,
		Name:    svc.Service,
		Address: svc.Address,
		Port:    svc.Port,
		Tags:    svc.Tags,
		Meta:    svc.Meta,
	}
}
