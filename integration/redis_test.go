//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/redis"
)

const redisAddr = "localhost:6379"

// TestRedis_SetGetDel connects to a real Redis instance, sets a JSON value,
// retrieves it, deletes it, and verifies the key is gone.
func TestRedis_SetGetDel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := redis.New(
		redis.WithAddr(redisAddr),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	type user struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	key := "integ:redis:user:1"
	want := user{Name: "Alice", Email: "alice@example.com"}

	// Set a value with TTL.
	err = client.Set(ctx, key, want, 30*time.Second)
	require.NoError(t, err)

	// Get the value back.
	var got user
	err = client.Get(ctx, key, &got)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	// Delete the key.
	err = client.Del(ctx, key)
	require.NoError(t, err)

	// Verify the key is gone.
	err = client.Get(ctx, key, &got)
	require.Error(t, err, "Get should return an error for a deleted key")
}

// TestRedis_SetNX verifies atomic set-if-not-exists. The first call should
// succeed; the second call with the same key should return false.
func TestRedis_SetNX(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := redis.New(
		redis.WithAddr(redisAddr),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	key := "integ:redis:setnx:" + t.Name()

	// Clean up in case of leftover from previous run.
	_ = client.Del(ctx, key)

	// First SetNX should succeed.
	ok, err := client.SetNX(ctx, key, "first", 30*time.Second)
	require.NoError(t, err)
	assert.True(t, ok, "first SetNX should succeed")

	// Second SetNX with the same key should return false.
	ok, err = client.SetNX(ctx, key, "second", 30*time.Second)
	require.NoError(t, err)
	assert.False(t, ok, "second SetNX should fail because key exists")

	// Clean up.
	_ = client.Del(ctx, key)
}

// TestRedis_HealthCheck verifies that HealthCheck returns nil for a healthy
// Redis connection.
func TestRedis_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := redis.New(
		redis.WithAddr(redisAddr),
	)
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() { require.NoError(t, client.Stop(ctx)) }()

	err = client.HealthCheck(ctx)
	assert.NoError(t, err, "HealthCheck should pass for a healthy Redis")
}

// TestRedis_HealthCheckNotStarted verifies that HealthCheck returns an error
// when the client has not been started.
func TestRedis_HealthCheckNotStarted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	client, err := redis.New(
		redis.WithAddr(redisAddr),
	)
	require.NoError(t, err)

	err = client.HealthCheck(context.Background())
	assert.Error(t, err, "HealthCheck should fail when client is not started")
}
