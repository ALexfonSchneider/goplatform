// Package postgres provides a PostgreSQL client built on pgx that implements
// platform.Component and platform.HealthChecker.
//
// DB wraps a pgxpool.Pool and manages the full connection lifecycle: connecting,
// health checking, and graceful shutdown. It supports OpenTelemetry tracing,
// query hooks for logging and metrics, and database migrations via golang-migrate.
//
// Usage:
//
//	db, _ := postgres.New(
//	    postgres.WithDSN("postgres://user:pass@localhost:5432/mydb"),
//	    postgres.WithMaxConns(20),
//	    postgres.WithLogger(logger),
//	)
//	app.Register("postgres", db)
//
//	// Use the pool directly:
//	rows, _ := db.Pool().Query(ctx, "SELECT id, name FROM users")
package postgres

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	"go.opentelemetry.io/otel/trace"
)

// Compile-time checks that DB implements platform.Component and platform.HealthChecker.
var (
	_ platform.Component     = (*DB)(nil)
	_ platform.HealthChecker = (*DB)(nil)
)

// DB wraps a pgxpool.Pool and implements platform.Component and platform.HealthChecker.
// It manages the full connection pool lifecycle: connecting on Start, health
// checking via Ping, and closing the pool on Stop.
//
// DB is safe for concurrent use. The underlying pgxpool.Pool handles connection
// pooling and is also safe for concurrent use.
type DB struct {
	mu sync.Mutex

	// Configuration set via options.
	dsn         string
	maxConns    int32
	minConns    int32
	connTimeout time.Duration
	logger      platform.Logger
	tp          trace.TracerProvider
	hooks       []QueryHook

	// Runtime state.
	pool *pgxpool.Pool
}

// Option configures a DB instance.
type Option func(*DB)

// WithDSN sets the PostgreSQL connection string (Data Source Name).
// The DSN must be a valid PostgreSQL connection URI, e.g.
// "postgres://user:password@host:5432/dbname?sslmode=disable".
func WithDSN(dsn string) Option {
	return func(db *DB) {
		db.dsn = dsn
	}
}

// WithMaxConns sets the maximum number of connections in the pool.
// If zero or negative, the pgxpool default is used.
func WithMaxConns(n int32) Option {
	return func(db *DB) {
		db.maxConns = n
	}
}

// WithMinConns sets the minimum number of idle connections maintained in the pool.
// If zero, no minimum idle connections are maintained.
func WithMinConns(n int32) Option {
	return func(db *DB) {
		db.minConns = n
	}
}

// WithConnTimeout sets the maximum duration to wait when establishing a new
// connection. If zero, the pgxpool default timeout is used.
func WithConnTimeout(d time.Duration) Option {
	return func(db *DB) {
		db.connTimeout = d
	}
}

// WithTracerProvider sets the OpenTelemetry TracerProvider used to create spans
// for database queries. If not set, no tracing spans are created.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(db *DB) {
		db.tp = tp
	}
}

// WithQueryHook registers one or more query hooks that are called after each
// query execution. Hooks receive the query SQL, arguments, duration, and any
// error. Multiple calls to WithQueryHook are additive.
func WithQueryHook(hooks ...QueryHook) Option {
	return func(db *DB) {
		db.hooks = append(db.hooks, hooks...)
	}
}

// WithLogger sets the structured logger used by DB for connection lifecycle
// events and internal diagnostics.
func WithLogger(l platform.Logger) Option {
	return func(db *DB) {
		db.logger = l
	}
}

// New creates a new DB with the given options. The returned DB is not yet
// connected — call Start to establish the connection pool.
func New(opts ...Option) (*DB, error) {
	db := &DB{
		logger: platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(db)
	}
	return db, nil
}

// Start connects to PostgreSQL and pings the database. It implements
// platform.Component.
//
// Start parses the DSN, applies pool configuration (max/min connections,
// connect timeout), attaches the query tracer, creates the connection pool,
// and verifies connectivity with a ping. If any step fails, Start returns
// an error wrapping ErrConnFailed.
func (db *DB) Start(ctx context.Context) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.pool != nil {
		return fmt.Errorf("postgres: already started")
	}

	cfg, err := pgxpool.ParseConfig(db.dsn)
	if err != nil {
		return fmt.Errorf("postgres: parse DSN: %w", fmt.Errorf("%w: %w", ErrConnFailed, err))
	}

	if db.maxConns > 0 {
		cfg.MaxConns = db.maxConns
	}
	if db.minConns > 0 {
		cfg.MinConns = db.minConns
	}
	if db.connTimeout > 0 {
		cfg.ConnConfig.ConnectTimeout = db.connTimeout
	}

	// Attach query tracer if hooks or tracing are configured.
	if len(db.hooks) > 0 || db.tp != nil {
		cfg.ConnConfig.Tracer = &queryTracer{
			hooks: db.hooks,
			tp:    db.tp,
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("postgres: create pool: %w", fmt.Errorf("%w: %w", ErrConnFailed, err))
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("postgres: ping: %w", fmt.Errorf("%w: %w", ErrConnFailed, err))
	}

	db.pool = pool
	db.logger.Info("postgres: connected", "dsn", maskDSNPassword(db.dsn))
	return nil
}

// Stop closes the connection pool. It implements platform.Component.
// Stop is safe to call multiple times; subsequent calls are no-ops.
func (db *DB) Stop(_ context.Context) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.pool == nil {
		return nil
	}

	db.pool.Close()
	db.pool = nil
	db.logger.Info("postgres: connection closed")
	return nil
}

// Pool returns the underlying *pgxpool.Pool for direct access to pgx
// query methods. Returns nil if the DB has not been started.
//
// This is an escape hatch for cases where the convenience methods on DB
// are not sufficient. The caller is responsible for using the pool correctly.
func (db *DB) Pool() *pgxpool.Pool {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.pool
}

// Ping verifies the database connection is alive by executing a ping against
// the connection pool. Returns an error wrapping ErrConnFailed if the pool
// is nil or the ping fails.
func (db *DB) Ping(ctx context.Context) error {
	db.mu.Lock()
	pool := db.pool
	db.mu.Unlock()

	if pool == nil {
		return fmt.Errorf("postgres: not connected: %w", ErrConnFailed)
	}

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: ping failed: %w", ErrConnFailed)
	}
	return nil
}

// HealthCheck implements platform.HealthChecker by delegating to Ping.
// It returns nil if the database is reachable, or an error otherwise.
func (db *DB) HealthCheck(ctx context.Context) error {
	return db.Ping(ctx)
}

// Config returns a copy of the internal configuration for testing purposes.
// This is not exported to avoid exposing internal state.
func (db *DB) config() dbConfig {
	return dbConfig{
		dsn:         db.dsn,
		maxConns:    db.maxConns,
		minConns:    db.minConns,
		connTimeout: db.connTimeout,
		hooksLen:    len(db.hooks),
		hasTP:       db.tp != nil,
		hasLogger:   db.logger != nil,
	}
}

// maskDSNPassword replaces the password in a PostgreSQL DSN with "****"
// to prevent credentials from appearing in logs.
func maskDSNPassword(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "****"
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "****")
		}
	}
	return u.String()
}

// dbConfig holds a snapshot of DB configuration for test assertions.
type dbConfig struct {
	dsn         string
	maxConns    int32
	minConns    int32
	connTimeout time.Duration
	hooksLen    int
	hasTP       bool
	hasLogger   bool
}
