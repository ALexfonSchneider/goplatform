package consul

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/discovery"
	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

func TestNew_DefaultConfig(t *testing.T) {
	r, err := New()
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.NotNil(t, r.client, "client must be initialised")
	assert.NotNil(t, r.logger, "logger must be initialised")
}

func TestNew_WithAddress(t *testing.T) {
	r, err := New(WithAddress("consul.example.com:8500"))
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestNew_WithToken(t *testing.T) {
	r, err := New(WithToken("my-secret-token"))
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestNew_WithLogger(t *testing.T) {
	logger := platform.NopLogger()
	r, err := New(WithLogger(logger))
	require.NoError(t, err)
	require.NotNil(t, r)
	assert.Equal(t, logger, r.logger)
}

func TestNew_AllOptions(t *testing.T) {
	r, err := New(
		WithAddress("10.0.0.1:8500"),
		WithToken("token"),
		WithLogger(platform.NopLogger()),
	)
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestRegistry_ImplementsInterface(t *testing.T) {
	// Verify at compile time via the package-level var _ check, and also
	// exercise it here to make intent explicit.
	var reg discovery.Registry = &Registry{}
	assert.NotNil(t, reg)
}
