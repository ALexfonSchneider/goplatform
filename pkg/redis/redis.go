// Package redis provides a Redis client built on go-redis that implements
// platform.Component and platform.HealthChecker.
//
// Client wraps a go-redis client and manages the full connection lifecycle:
// connecting and pinging on Start, health checking via Ping, and closing the
// connection on Stop. It provides convenient JSON-based Set/Get operations
// and an atomic SetNX for use as an idempotency store.
//
// Usage:
//
//	c, _ := redis.New(
//	    redis.WithAddr("localhost:6379"),
//	    redis.WithPassword("secret"),
//	    redis.WithLogger(logger),
//	)
//	app.Register("redis", c)
//
//	// Store and retrieve values:
//	_ = c.Set(ctx, "user:1", myStruct, 5*time.Minute)
//	_ = c.Get(ctx, "user:1", &dest)
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time checks that Client implements platform.Component and
// platform.HealthChecker.
var (
	_ platform.Component     = (*Client)(nil)
	_ platform.HealthChecker = (*Client)(nil)
)

// Client wraps a go-redis client and implements platform.Component and
// platform.HealthChecker. It provides JSON-based Set/Get operations and an
// atomic SetNX that matches the server.IdempotencyStore interface via
// structural (duck) typing.
//
// Client is safe for concurrent use. The underlying go-redis client handles
// connection pooling and is also safe for concurrent use.
type Client struct {
	mu sync.Mutex

	// Configuration set via options.
	addr     string
	password string
	db       int
	logger   platform.Logger

	// Runtime state.
	rdb *goredis.Client
}

// Option configures a Client instance.
type Option func(*Client)

// WithAddr sets the Redis server address in "host:port" format.
// The default address is "localhost:6379".
func WithAddr(addr string) Option {
	return func(c *Client) {
		c.addr = addr
	}
}

// WithPassword sets the password used to authenticate with the Redis server.
// If empty, no authentication is performed.
func WithPassword(password string) Option {
	return func(c *Client) {
		c.password = password
	}
}

// WithDB sets the Redis database number to select after connecting.
// The default is 0.
func WithDB(db int) Option {
	return func(c *Client) {
		c.db = db
	}
}

// WithLogger sets the structured logger used by Client for connection
// lifecycle events and internal diagnostics.
func WithLogger(l platform.Logger) Option {
	return func(c *Client) {
		c.logger = l
	}
}

// New creates a new Client with the given options. The returned Client is not
// yet connected — call Start to establish the connection.
//
// Default values:
//   - addr: "localhost:6379"
//   - password: "" (no authentication)
//   - db: 0
//   - logger: platform.NopLogger()
func New(opts ...Option) (*Client, error) {
	c := &Client{
		addr:   "localhost:6379",
		logger: platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Start connects to Redis and pings the server to verify connectivity.
// It implements platform.Component.
//
// Start creates the underlying go-redis client and issues a PING command.
// If the ping fails, the connection is closed and an error is returned.
// Calling Start on an already-started Client returns an error.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rdb != nil {
		return fmt.Errorf("redis: already started")
	}

	rdb := goredis.NewClient(&goredis.Options{
		Addr:         c.addr,
		Password:     c.password,
		DB:           c.db,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return fmt.Errorf("redis: ping failed: %w", err)
	}

	c.rdb = rdb
	c.logger.Info("redis: connected", "addr", c.addr, "db", c.db)
	return nil
}

// Stop closes the Redis connection. It implements platform.Component.
// Stop is safe to call multiple times; subsequent calls are no-ops.
func (c *Client) Stop(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rdb == nil {
		return nil
	}

	err := c.rdb.Close()
	c.rdb = nil
	if err != nil {
		return fmt.Errorf("redis: close failed: %w", err)
	}

	c.logger.Info("redis: connection closed")
	return nil
}

// HealthCheck pings Redis to verify the connection is alive.
// It implements platform.HealthChecker.
// Returns nil if the server responds, or an error if the ping fails
// or the client has not been started.
func (c *Client) HealthCheck(ctx context.Context) error {
	c.mu.Lock()
	rdb := c.rdb
	c.mu.Unlock()

	if rdb == nil {
		return fmt.Errorf("redis: not connected")
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: health check failed: %w", err)
	}
	return nil
}

// Set stores a value in Redis with the given TTL. The value is
// JSON-marshaled before storage. A TTL of zero means the key never expires.
//
// Set returns an error if JSON marshaling fails, the client is not started,
// or the Redis command fails.
func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	c.mu.Lock()
	rdb := c.rdb
	c.mu.Unlock()

	if rdb == nil {
		return fmt.Errorf("redis: not connected")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("redis: marshal value: %w", err)
	}

	if err := rdb.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis: set key %q: %w", key, err)
	}
	return nil
}

// Get retrieves a value from Redis and JSON-unmarshals it into dest.
// dest must be a pointer to the target type.
//
// Get returns ErrKeyNotFound if the key does not exist. It returns an error
// if the client is not started, the Redis command fails, or JSON unmarshaling
// fails.
func (c *Client) Get(ctx context.Context, key string, dest any) error {
	c.mu.Lock()
	rdb := c.rdb
	c.mu.Unlock()

	if rdb == nil {
		return fmt.Errorf("redis: not connected")
	}

	data, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return fmt.Errorf("redis: get key %q: %w", key, ErrKeyNotFound)
		}
		return fmt.Errorf("redis: get key %q: %w", key, err)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("redis: unmarshal key %q: %w", key, err)
	}
	return nil
}

// SetNX sets the value only if the key does not already exist. It returns true
// if the value was set (key was new) and false if the key already existed.
// The value is JSON-marshaled before storage.
//
// SetNX matches the server.IdempotencyStore interface via structural (duck)
// typing, allowing Client to be used as an idempotency store without importing
// the server package.
//
// SetNX returns an error if JSON marshaling fails, the client is not started,
// or the Redis command fails.
func (c *Client) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	rdb := c.rdb
	c.mu.Unlock()

	if rdb == nil {
		return false, fmt.Errorf("redis: not connected")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("redis: marshal value: %w", err)
	}

	err = rdb.SetArgs(ctx, key, data, goredis.SetArgs{
		Mode: "NX",
		TTL:  ttl,
	}).Err()
	if errors.Is(err, goredis.Nil) {
		// Key already existed; SetArgs with NX returns redis.Nil.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis: setnx key %q: %w", key, err)
	}
	return true, nil
}

// Del deletes one or more keys from Redis. Keys that do not exist are ignored.
//
// Del returns an error if the client is not started or the Redis command fails.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	c.mu.Lock()
	rdb := c.rdb
	c.mu.Unlock()

	if rdb == nil {
		return fmt.Errorf("redis: not connected")
	}

	if err := rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis: del keys: %w", err)
	}
	return nil
}

// Unwrap returns the underlying *redis.Client from go-redis for direct access
// to any Redis command not covered by the convenience methods (lists, hashes,
// sets, sorted sets, streams, pub/sub, Lua scripts, pipelines, etc.).
// Returns nil if the client has not been started.
//
// Usage:
//
//	rdb := c.Unwrap()
//	rdb.LPush(ctx, "queue", "item")
//	rdb.HSet(ctx, "user:1", "name", "Alice")
//	pipe := rdb.Pipeline()
func (c *Client) Unwrap() *goredis.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rdb
}
