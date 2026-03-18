package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// testUser is a sample struct used for JSON serialization tests.
type testUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
}

// startTestClient creates a miniredis instance and a connected Client for testing.
// It registers cleanup to close both the client and the miniredis server.
func startTestClient(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()

	mr := miniredis.RunT(t)

	c, err := New(
		WithAddr(mr.Addr()),
		WithLogger(platform.NopLogger()),
	)
	require.NoError(t, err)

	err = c.Start(context.Background())
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = c.Stop(context.Background())
	})

	return c, mr
}

func TestClient_SetGet(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	err := c.Set(ctx, "greeting", "hello world", time.Minute)
	require.NoError(t, err)

	var got string
	err = c.Get(ctx, "greeting", &got)
	require.NoError(t, err)
	assert.Equal(t, "hello world", got)
}

func TestClient_SetNX(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	// First SetNX should succeed (key is new).
	ok, err := c.SetNX(ctx, "unique-key", "first", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok, "SetNX should return true for a new key")

	// Second SetNX with the same key should not overwrite.
	ok, err = c.SetNX(ctx, "unique-key", "second", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok, "SetNX should return false for an existing key")

	// Verify the original value is preserved.
	var got string
	err = c.Get(ctx, "unique-key", &got)
	require.NoError(t, err)
	assert.Equal(t, "first", got)
}

func TestClient_Del(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	err := c.Set(ctx, "to-delete", "value", time.Minute)
	require.NoError(t, err)

	// Verify the key exists.
	var got string
	err = c.Get(ctx, "to-delete", &got)
	require.NoError(t, err)
	assert.Equal(t, "value", got)

	// Delete the key.
	err = c.Del(ctx, "to-delete")
	require.NoError(t, err)

	// Get should now return ErrKeyNotFound.
	err = c.Get(ctx, "to-delete", &got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound), "expected ErrKeyNotFound, got: %v", err)
}

func TestClient_TTLExpiry(t *testing.T) {
	c, mr := startTestClient(t)
	ctx := context.Background()

	err := c.Set(ctx, "expires-soon", "temporary", 2*time.Second)
	require.NoError(t, err)

	// Value should be retrievable before expiry.
	var got string
	err = c.Get(ctx, "expires-soon", &got)
	require.NoError(t, err)
	assert.Equal(t, "temporary", got)

	// Fast-forward the miniredis clock past the TTL.
	mr.FastForward(3 * time.Second)

	// Value should now be gone.
	err = c.Get(ctx, "expires-soon", &got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound), "expected ErrKeyNotFound after TTL expiry, got: %v", err)
}

func TestClient_JSONSerialization(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	original := testUser{
		Name:  "Alice",
		Email: "alice@example.com",
		Age:   30,
	}

	err := c.Set(ctx, "user:1", original, time.Minute)
	require.NoError(t, err)

	var restored testUser
	err = c.Get(ctx, "user:1", &restored)
	require.NoError(t, err)

	assert.Equal(t, original.Name, restored.Name)
	assert.Equal(t, original.Email, restored.Email)
	assert.Equal(t, original.Age, restored.Age)
}

func TestClient_StartStop(t *testing.T) {
	mr := miniredis.RunT(t)

	c, err := New(
		WithAddr(mr.Addr()),
		WithLogger(platform.NopLogger()),
	)
	require.NoError(t, err)

	// Start should connect and ping successfully.
	err = c.Start(context.Background())
	require.NoError(t, err)

	// HealthCheck should succeed after Start.
	err = c.HealthCheck(context.Background())
	assert.NoError(t, err)

	// Stop should close the connection cleanly.
	err = c.Stop(context.Background())
	assert.NoError(t, err)

	// Stop again should be a no-op.
	err = c.Stop(context.Background())
	assert.NoError(t, err)
}

func TestClient_GetKeyNotFound(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	var got string
	err := c.Get(ctx, "nonexistent", &got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKeyNotFound), "expected ErrKeyNotFound, got: %v", err)
}

func TestClient_HealthCheckBeforeStart(t *testing.T) {
	c, err := New()
	require.NoError(t, err)

	err = c.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

func TestClient_DoubleStart(t *testing.T) {
	c, _ := startTestClient(t)

	err := c.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

func TestClient_Options(t *testing.T) {
	c, err := New(
		WithAddr("redis.example.com:6380"),
		WithPassword("secret"),
		WithDB(3),
		WithLogger(platform.NopLogger()),
	)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "redis.example.com:6380", c.addr)
	assert.Equal(t, "secret", c.password)
	assert.Equal(t, 3, c.db)
}

func TestClient_Defaults(t *testing.T) {
	c, err := New()
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "localhost:6379", c.addr)
	assert.Equal(t, "", c.password)
	assert.Equal(t, 0, c.db)
	assert.NotNil(t, c.logger)
}

func TestClient_ImplementsInterfaces(t *testing.T) {
	// These are also checked at compile time via the var _ declarations,
	// but explicit test assertions make the requirement visible in test output.
	var client interface{} = &Client{}
	_, ok := client.(platform.Component)
	assert.True(t, ok, "Client should implement platform.Component")
	_, ok = client.(platform.HealthChecker)
	assert.True(t, ok, "Client should implement platform.HealthChecker")
}

func TestClient_DelMultipleKeys(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	// Set multiple keys.
	require.NoError(t, c.Set(ctx, "k1", "v1", time.Minute))
	require.NoError(t, c.Set(ctx, "k2", "v2", time.Minute))
	require.NoError(t, c.Set(ctx, "k3", "v3", time.Minute))

	// Delete two of them at once.
	err := c.Del(ctx, "k1", "k2")
	require.NoError(t, err)

	// k1 and k2 should be gone.
	var got string
	assert.True(t, errors.Is(c.Get(ctx, "k1", &got), ErrKeyNotFound))
	assert.True(t, errors.Is(c.Get(ctx, "k2", &got), ErrKeyNotFound))

	// k3 should still exist.
	err = c.Get(ctx, "k3", &got)
	require.NoError(t, err)
	assert.Equal(t, "v3", got)
}

func TestClient_DelNonexistentKey(t *testing.T) {
	c, _ := startTestClient(t)
	ctx := context.Background()

	// Deleting a key that doesn't exist should not error.
	err := c.Del(ctx, "does-not-exist")
	assert.NoError(t, err)
}
