//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcconsul "github.com/testcontainers/testcontainers-go/modules/consul"

	"github.com/ALexfonSchneider/goplatform/pkg/discovery"
	"github.com/ALexfonSchneider/goplatform/pkg/discovery/consul"
)

func TestTC_Consul_RegisterDiscover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcconsul.Run(ctx, "hashicorp/consul:1.17")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	endpoint, err := ctr.ApiEndpoint(ctx)
	require.NoError(t, err)

	reg, err := consul.New(consul.WithAddress(endpoint))
	require.NoError(t, err)

	inst := discovery.Instance{
		ID:      "svc-1",
		Name:    "my-service",
		Address: "10.0.0.1",
		Port:    8080,
	}

	// Register the instance.
	require.NoError(t, reg.Register(ctx, inst))

	// Discover should return the registered instance.
	instances, err := reg.Discover(ctx, "my-service")
	require.NoError(t, err)
	require.Len(t, instances, 1)

	assert.Equal(t, "svc-1", instances[0].ID)
	assert.Equal(t, "my-service", instances[0].Name)
	assert.Equal(t, "10.0.0.1", instances[0].Address)
	assert.Equal(t, 8080, instances[0].Port)

	// Deregister the instance.
	require.NoError(t, reg.Deregister(ctx, "svc-1"))

	// Discover after deregister should return empty.
	instances, err = reg.Discover(ctx, "my-service")
	require.NoError(t, err)
	assert.Empty(t, instances)
}
