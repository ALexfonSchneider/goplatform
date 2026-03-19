//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/discovery"
	"github.com/ALexfonSchneider/goplatform/pkg/discovery/consul"
)

const consulAddr = "localhost:8500"

// TestConsul_RegisterDiscoverDeregister registers a service with Consul,
// discovers it via the health-aware API, and then deregisters it.
func TestConsul_RegisterDiscoverDeregister(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, err := consul.New(
		consul.WithAddress(consulAddr),
	)
	require.NoError(t, err)

	inst := discovery.Instance{
		ID:      "integ-test-svc-1",
		Name:    "integ-test-svc",
		Address: "127.0.0.1",
		Port:    9999,
		Tags:    []string{"integ", "test"},
		Meta:    map[string]string{"version": "1.0.0"},
	}

	// Register.
	err = registry.Register(ctx, inst)
	require.NoError(t, err)

	// Discover — should find at least our instance.
	instances, err := registry.Discover(ctx, "integ-test-svc")
	require.NoError(t, err)
	require.NotEmpty(t, instances, "should discover at least one instance")

	var found bool
	for _, i := range instances {
		if i.ID == inst.ID {
			found = true
			assert.Equal(t, inst.Name, i.Name)
			assert.Equal(t, inst.Address, i.Address)
			assert.Equal(t, inst.Port, i.Port)
			break
		}
	}
	assert.True(t, found, "should find our registered instance")

	// Deregister.
	err = registry.Deregister(ctx, inst.ID)
	require.NoError(t, err)

	// After deregister, discovery should not find the instance.
	instances, err = registry.Discover(ctx, "integ-test-svc")
	require.NoError(t, err)

	for _, i := range instances {
		assert.NotEqual(t, inst.ID, i.ID, "deregistered instance should not be discoverable")
	}
}

// TestConsul_Watch registers a service and uses Watch to observe the change.
func TestConsul_Watch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	registry, err := consul.New(
		consul.WithAddress(consulAddr),
	)
	require.NoError(t, err)

	serviceName := "integ-watch-svc"

	// Start watching before registering.
	ch, err := registry.Watch(ctx, serviceName)
	require.NoError(t, err)

	// Wait for the initial empty snapshot from Watch.
	select {
	case instances := <-ch:
		// Initial state may be empty.
		_ = instances
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial Watch snapshot")
	}

	// Register a service instance.
	inst := discovery.Instance{
		ID:      "integ-watch-1",
		Name:    serviceName,
		Address: "127.0.0.1",
		Port:    8888,
	}
	err = registry.Register(ctx, inst)
	require.NoError(t, err)
	defer func() { _ = registry.Deregister(ctx, inst.ID) }()

	// Wait for Watch to emit the updated instance list.
	var found bool
	for i := 0; i < 5; i++ {
		select {
		case instances := <-ch:
			for _, ins := range instances {
				if ins.ID == inst.ID {
					found = true
					break
				}
			}
			if found {
				break
			}
		case <-time.After(5 * time.Second):
			continue
		}
		if found {
			break
		}
	}

	assert.True(t, found, "Watch should emit updated instances after registration")
}
