package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	nooptrace "go.opentelemetry.io/otel/trace/noop"
)

func TestNew_Options(t *testing.T) {
	hook := func(_ context.Context, _ QueryHookData) {}
	tp := nooptrace.NewTracerProvider()
	logger := platform.NopLogger()

	db, err := New(
		WithDSN("postgres://user:pass@localhost:5432/testdb"),
		WithMaxConns(20),
		WithMinConns(5),
		WithConnTimeout(30*time.Second),
		WithTracerProvider(tp),
		WithQueryHook(hook),
		WithLogger(logger),
	)
	require.NoError(t, err)
	require.NotNil(t, db)

	cfg := db.config()
	assert.Equal(t, "postgres://user:pass@localhost:5432/testdb", cfg.dsn)
	assert.Equal(t, int32(20), cfg.maxConns)
	assert.Equal(t, int32(5), cfg.minConns)
	assert.Equal(t, 30*time.Second, cfg.connTimeout)
	assert.Equal(t, 1, cfg.hooksLen)
	assert.True(t, cfg.hasTP)
	assert.True(t, cfg.hasLogger)
}

func TestNew_Defaults(t *testing.T) {
	db, err := New()
	require.NoError(t, err)
	require.NotNil(t, db)

	cfg := db.config()
	assert.Empty(t, cfg.dsn)
	assert.Equal(t, int32(0), cfg.maxConns)
	assert.Equal(t, int32(0), cfg.minConns)
	assert.Equal(t, time.Duration(0), cfg.connTimeout)
	assert.Equal(t, 0, cfg.hooksLen)
	assert.False(t, cfg.hasTP)
	// Default logger is NopLogger, not nil.
	assert.True(t, cfg.hasLogger)
}

func TestNew_MultipleQueryHooks(t *testing.T) {
	hook1 := func(_ context.Context, _ QueryHookData) {}
	hook2 := func(_ context.Context, _ QueryHookData) {}
	hook3 := func(_ context.Context, _ QueryHookData) {}

	db, err := New(
		WithQueryHook(hook1, hook2),
		WithQueryHook(hook3),
	)
	require.NoError(t, err)

	cfg := db.config()
	assert.Equal(t, 3, cfg.hooksLen)
}

func TestSentinelErrors(t *testing.T) {
	t.Run("ErrConnFailed is defined", func(t *testing.T) {
		assert.NotNil(t, ErrConnFailed)
		assert.Equal(t, "postgres: connection failed", ErrConnFailed.Error())
	})

	t.Run("ErrTxRollback is defined", func(t *testing.T) {
		assert.NotNil(t, ErrTxRollback)
		assert.Equal(t, "postgres: transaction rolled back", ErrTxRollback.Error())
	})

	t.Run("ErrConnFailed is checkable via errors.Is", func(t *testing.T) {
		wrapped := errors.Join(ErrConnFailed, errors.New("underlying cause"))
		assert.True(t, errors.Is(wrapped, ErrConnFailed))
	})

	t.Run("ErrTxRollback is checkable via errors.Is", func(t *testing.T) {
		wrapped := errors.Join(ErrTxRollback, errors.New("underlying cause"))
		assert.True(t, errors.Is(wrapped, ErrTxRollback))
	})
}

func TestQueryHookData(t *testing.T) {
	data := QueryHookData{
		SQL:      "SELECT 1",
		Args:     []any{42, "hello"},
		Duration: 100 * time.Millisecond,
		Err:      errors.New("test error"),
	}

	assert.Equal(t, "SELECT 1", data.SQL)
	assert.Equal(t, []any{42, "hello"}, data.Args)
	assert.Equal(t, 100*time.Millisecond, data.Duration)
	assert.EqualError(t, data.Err, "test error")
}

func TestDB_ImplementsInterfaces(t *testing.T) {
	// These are also checked at compile time via the var _ declarations,
	// but explicit test assertions make the requirement visible in test output.
	var db interface{} = &DB{}
	_, ok := db.(platform.Component)
	assert.True(t, ok, "DB should implement platform.Component")
	_, ok = db.(platform.HealthChecker)
	assert.True(t, ok, "DB should implement platform.HealthChecker")
}

func TestDB_PoolNilBeforeStart(t *testing.T) {
	db, err := New(WithDSN("postgres://localhost:5432/test"))
	require.NoError(t, err)
	assert.Nil(t, db.Pool())
}

func TestDB_PingFailsBeforeStart(t *testing.T) {
	db, err := New(WithDSN("postgres://localhost:5432/test"))
	require.NoError(t, err)

	err = db.Ping(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnFailed))
}

func TestDB_HealthCheckFailsBeforeStart(t *testing.T) {
	db, err := New(WithDSN("postgres://localhost:5432/test"))
	require.NoError(t, err)

	err = db.HealthCheck(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnFailed))
}

func TestDB_StopBeforeStart(t *testing.T) {
	db, err := New()
	require.NoError(t, err)

	// Stop without Start should be a no-op.
	err = db.Stop(context.Background())
	assert.NoError(t, err)
}

func TestDB_WithTxFailsBeforeStart(t *testing.T) {
	db, err := New()
	require.NoError(t, err)

	err = db.WithTx(context.Background(), func(_ pgx.Tx) error {
		return nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConnFailed))
}
