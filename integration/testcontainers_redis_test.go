//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/ALexfonSchneider/goplatform/pkg/redis"
)

func TestTC_Redis_SetGetDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcredis.Run(ctx, "redis:7-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	connStr, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	addr := strings.TrimPrefix(connStr, "redis://")

	c, err := redis.New(redis.WithAddr(addr))
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	// Set a value.
	require.NoError(t, c.Set(ctx, "key1", "hello", 0))

	// Get the value back.
	var got string
	require.NoError(t, c.Get(ctx, "key1", &got))
	assert.Equal(t, "hello", got)

	// Delete the key.
	require.NoError(t, c.Del(ctx, "key1"))

	// Get after delete should return ErrKeyNotFound.
	err = c.Get(ctx, "key1", &got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, redis.ErrKeyNotFound), "expected ErrKeyNotFound, got: %v", err)
}

func TestTC_Redis_SetNX(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcredis.Run(ctx, "redis:7-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	connStr, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	addr := strings.TrimPrefix(connStr, "redis://")

	c, err := redis.New(redis.WithAddr(addr))
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	// First SetNX should succeed.
	ok, err := c.SetNX(ctx, "nx-key", "first", 10*time.Second)
	require.NoError(t, err)
	assert.True(t, ok)

	// Second SetNX with same key should return false.
	ok, err = c.SetNX(ctx, "nx-key", "second", 10*time.Second)
	require.NoError(t, err)
	assert.False(t, ok)

	// Value should be the first one set.
	var got string
	require.NoError(t, c.Get(ctx, "nx-key", &got))
	assert.Equal(t, "first", got)
}

func TestTC_Redis_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcredis.Run(ctx, "redis:7-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	connStr, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	addr := strings.TrimPrefix(connStr, "redis://")

	c, err := redis.New(redis.WithAddr(addr))
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	type User struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	input := User{Name: "Alice", Age: 30}
	require.NoError(t, c.Set(ctx, "user:1", input, 0))

	var got User
	require.NoError(t, c.Get(ctx, "user:1", &got))
	assert.Equal(t, input, got)
}

func TestTC_Redis_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcredis.Run(ctx, "redis:7-alpine")
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	connStr, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	addr := strings.TrimPrefix(connStr, "redis://")

	c, err := redis.New(redis.WithAddr(addr))
	require.NoError(t, err)
	require.NoError(t, c.Start(ctx))
	defer func() { require.NoError(t, c.Stop(ctx)) }()

	require.NoError(t, c.HealthCheck(ctx))
}
