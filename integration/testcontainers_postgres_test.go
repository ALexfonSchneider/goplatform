//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ALexfonSchneider/goplatform/pkg/postgres"
)

func TestTC_Postgres_WithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := postgres.New(postgres.WithDSN(dsn))
	require.NoError(t, err)
	require.NoError(t, db.Start(ctx))
	defer func() { require.NoError(t, db.Stop(ctx)) }()

	pool := db.Pool()
	_, err = pool.Exec(ctx, "CREATE TABLE test_items (id SERIAL PRIMARY KEY, name TEXT NOT NULL)")
	require.NoError(t, err)

	// Test commit
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO test_items (name) VALUES ($1)", "committed")
		return err
	})
	require.NoError(t, err)

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM test_items WHERE name='committed'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Test rollback
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO test_items (name) VALUES ($1)", "rolled_back")
		if err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	require.Error(t, err)

	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM test_items WHERE name='rolled_back'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestTC_Postgres_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, ctr)
	require.NoError(t, err)

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := postgres.New(postgres.WithDSN(dsn))
	require.NoError(t, err)
	require.NoError(t, db.Start(ctx))
	defer func() { require.NoError(t, db.Stop(ctx)) }()

	require.NoError(t, db.HealthCheck(ctx))
}
