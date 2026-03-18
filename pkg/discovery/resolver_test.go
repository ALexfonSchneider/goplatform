package discovery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRegistry is a test double for the Registry interface. It returns
// preconfigured instances from Discover and provides stub implementations
// for Register, Deregister, and Watch.
type mockRegistry struct {
	mu        sync.Mutex
	instances []Instance
}

// Register is a no-op stub.
func (m *mockRegistry) Register(_ context.Context, _ Instance) error {
	return nil
}

// Deregister is a no-op stub.
func (m *mockRegistry) Deregister(_ context.Context, _ string) error {
	return nil
}

// Discover returns the currently configured instances.
func (m *mockRegistry) Discover(_ context.Context, _ string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]Instance, len(m.instances))
	copy(result, m.instances)
	return result, nil
}

// Watch returns a closed channel immediately; background watching is not
// exercised in these unit tests.
func (m *mockRegistry) Watch(_ context.Context, _ string) (<-chan []Instance, error) {
	ch := make(chan []Instance)
	close(ch)
	return ch, nil
}

// setInstances replaces the instances returned by Discover.
func (m *mockRegistry) setInstances(instances []Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.instances = make([]Instance, len(instances))
	copy(m.instances, instances)
}

func TestResolver_RoundRobin(t *testing.T) {
	instances := []Instance{
		{ID: "a", Name: "svc", Address: "10.0.0.1", Port: 8080},
		{ID: "b", Name: "svc", Address: "10.0.0.2", Port: 8080},
		{ID: "c", Name: "svc", Address: "10.0.0.3", Port: 8080},
	}

	mock := &mockRegistry{instances: instances}
	resolver := NewResolver(mock, WithTTL(1*time.Minute))

	ctx := context.Background()

	// Pick 6 times — each of 3 instances should be picked exactly twice in
	// round-robin order.
	picked := make(map[string]int)

	for i := 0; i < 6; i++ {
		inst, err := resolver.Pick(ctx, "svc")
		require.NoError(t, err)
		picked[inst.ID]++
	}

	assert.Equal(t, 2, picked["a"], "instance a should be picked twice")
	assert.Equal(t, 2, picked["b"], "instance b should be picked twice")
	assert.Equal(t, 2, picked["c"], "instance c should be picked twice")
}

func TestResolver_DeadInstanceRemoved(t *testing.T) {
	instances := []Instance{
		{ID: "a", Name: "svc", Address: "10.0.0.1", Port: 8080},
		{ID: "b", Name: "svc", Address: "10.0.0.2", Port: 8080},
		{ID: "c", Name: "svc", Address: "10.0.0.3", Port: 8080},
	}

	mock := &mockRegistry{instances: instances}

	// Use a very short TTL so the cache expires quickly.
	resolver := NewResolver(mock, WithTTL(1*time.Millisecond))

	ctx := context.Background()

	// First pick populates the cache.
	_, err := resolver.Pick(ctx, "svc")
	require.NoError(t, err)

	// Simulate instance "c" going down.
	mock.setInstances([]Instance{
		{ID: "a", Name: "svc", Address: "10.0.0.1", Port: 8080},
		{ID: "b", Name: "svc", Address: "10.0.0.2", Port: 8080},
	})

	// Wait for the cache to expire.
	time.Sleep(5 * time.Millisecond)

	// Pick several times — only instances a and b should appear.
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		inst, err := resolver.Pick(ctx, "svc")
		require.NoError(t, err)
		seen[inst.ID] = true
	}

	assert.True(t, seen["a"], "instance a should be in rotation")
	assert.True(t, seen["b"], "instance b should be in rotation")
	assert.False(t, seen["c"], "dead instance c should not be in rotation")
}

func TestResolver_EmptyInstances(t *testing.T) {
	mock := &mockRegistry{instances: nil}
	resolver := NewResolver(mock)

	ctx := context.Background()

	_, err := resolver.Pick(ctx, "svc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no instances available")
}

func TestResolver_StartStop(t *testing.T) {
	instances := []Instance{
		{ID: "a", Name: "svc", Address: "10.0.0.1", Port: 8080},
	}

	mock := &mockRegistry{instances: instances}
	resolver := NewResolver(mock,
		WithServices("svc"),
		WithTTL(1*time.Minute),
	)

	ctx := context.Background()

	err := resolver.Start(ctx)
	require.NoError(t, err)

	err = resolver.Stop(ctx)
	require.NoError(t, err)
}

func TestResolver_Options(t *testing.T) {
	mock := &mockRegistry{}

	t.Run("default TTL", func(t *testing.T) {
		r := NewResolver(mock)
		assert.Equal(t, 30*time.Second, r.ttl)
	})

	t.Run("custom TTL", func(t *testing.T) {
		r := NewResolver(mock, WithTTL(5*time.Minute))
		assert.Equal(t, 5*time.Minute, r.ttl)
	})

	t.Run("with services", func(t *testing.T) {
		r := NewResolver(mock, WithServices("svc-a", "svc-b"))
		assert.Equal(t, []string{"svc-a", "svc-b"}, r.services)
	})

	t.Run("with logger", func(t *testing.T) {
		r := NewResolver(mock, WithResolverLogger(nil))
		assert.Nil(t, r.logger)
	})
}
