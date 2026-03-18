package discovery

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time check that Resolver implements platform.Component.
var _ platform.Component = (*Resolver)(nil)

// Resolver provides client-side load balancing across service instances.
// It maintains a TTL-based cache of instances and uses round-robin selection
// to distribute requests. Background Watch-based refresh can be started for
// known services via Start, making Resolver a platform.Component.
//
// Resolver is safe for concurrent use.
type Resolver struct {
	reg    Registry
	ttl    time.Duration
	logger platform.Logger

	// services lists the service names to watch in background mode.
	services []string

	mu     sync.RWMutex
	caches map[string]*serviceCache

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// serviceCache holds cached instances for a single service along with a
// round-robin index and a timestamp indicating when the cache was last
// refreshed.
type serviceCache struct {
	mu         sync.RWMutex
	instances  []Instance
	lastUpdate time.Time
	index      atomic.Uint64
}

// ResolverOption configures a Resolver.
type ResolverOption func(*Resolver)

// NewResolver creates a new Resolver that uses the given Registry for
// instance discovery. Options can be used to configure cache TTL, watched
// services, and logging. The resolver is not started until Start is called;
// however, Pick can be used immediately (it falls back to synchronous
// Discover calls when the cache is empty or expired).
func NewResolver(reg Registry, opts ...ResolverOption) *Resolver {
	r := &Resolver{
		reg:    reg,
		ttl:    30 * time.Second,
		logger: platform.NopLogger(),
		caches: make(map[string]*serviceCache),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// WithTTL sets the cache TTL for the resolver. Cached instances older than the
// TTL are considered expired and a synchronous Discover call is made on the
// next Pick. The default TTL is 30 seconds.
func WithTTL(d time.Duration) ResolverOption {
	return func(r *Resolver) {
		r.ttl = d
	}
}

// WithServices sets the service names that the resolver watches in background
// mode. When Start is called, a Watch goroutine is launched for each service
// name, keeping the cache continuously up to date.
func WithServices(names ...string) ResolverOption {
	return func(r *Resolver) {
		r.services = names
	}
}

// WithResolverLogger sets the structured logger for the resolver.
// If not set, a no-op logger is used.
func WithResolverLogger(l platform.Logger) ResolverOption {
	return func(r *Resolver) {
		r.logger = l
	}
}

// Pick returns the next service instance using round-robin selection.
// If the cache is empty or expired, it performs a synchronous Discover call
// to refresh it. Pick returns an error if no healthy instances are available.
func (r *Resolver) Pick(ctx context.Context, service string) (Instance, error) {
	sc := r.getOrCreateCache(service)

	sc.mu.RLock()
	instances := sc.instances
	valid := !sc.lastUpdate.IsZero() && time.Since(sc.lastUpdate) < r.ttl
	sc.mu.RUnlock()

	if !valid || len(instances) == 0 {
		discovered, err := r.reg.Discover(ctx, service)
		if err != nil {
			return Instance{}, fmt.Errorf("discovery: resolve %q: %w", service, err)
		}

		sc.mu.Lock()
		sc.instances = discovered
		sc.lastUpdate = time.Now()
		instances = sc.instances
		sc.mu.Unlock()
	}

	if len(instances) == 0 {
		return Instance{}, fmt.Errorf("discovery: no instances available for %q", service)
	}

	idx := sc.index.Add(1) - 1
	return instances[idx%uint64(len(instances))], nil
}

// Start begins background Watch-based refresh for each service configured via
// WithServices. It implements platform.Component.
func (r *Resolver) Start(ctx context.Context) error {
	ctx, r.cancel = context.WithCancel(ctx)

	for _, svc := range r.services {
		ch, err := r.reg.Watch(ctx, svc)
		if err != nil {
			r.cancel()
			return fmt.Errorf("discovery: watch %q: %w", svc, err)
		}

		sc := r.getOrCreateCache(svc)
		r.wg.Add(1)

		go func(name string, ch <-chan []Instance, sc *serviceCache) {
			defer r.wg.Done()
			r.logger.Info("discovery: watcher started", "service", name)

			for instances := range ch {
				sc.mu.Lock()
				sc.instances = instances
				sc.lastUpdate = time.Now()
				sc.mu.Unlock()

				r.logger.Debug("discovery: cache updated",
					"service", name,
					"instances", len(instances),
				)
			}

			r.logger.Info("discovery: watcher stopped", "service", name)
		}(svc, ch, sc)
	}

	return nil
}

// Stop cancels all background watchers and waits for them to finish.
// It implements platform.Component.
func (r *Resolver) Stop(_ context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	return nil
}

// getOrCreateCache returns the serviceCache for the given service name,
// creating one if it does not exist.
func (r *Resolver) getOrCreateCache(service string) *serviceCache {
	r.mu.RLock()
	sc, ok := r.caches[service]
	r.mu.RUnlock()

	if ok {
		return sc
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if sc, ok = r.caches[service]; ok {
		return sc
	}

	sc = &serviceCache{}
	r.caches[service] = sc
	return sc
}
