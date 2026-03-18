//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/postgres"
)

const testDSN = "postgres://platform:platform@localhost:5432/platform?sslmode=disable"

// TestPostgres_WithTx_CommitAndRollback connects to a real PostgreSQL instance.
// It creates a table, inserts a row inside a committed transaction and verifies
// persistence, then inserts another row inside a transaction that returns an
// error and verifies the row was rolled back.
func TestPostgres_WithTx_CommitAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := postgres.New(
		postgres.WithDSN(testDSN),
	)
	require.NoError(t, err)
	require.NoError(t, db.Start(ctx))
	defer func() { require.NoError(t, db.Stop(ctx)) }()

	tableName := fmt.Sprintf("test_tx_%d", time.Now().UnixNano())

	// Create test table.
	_, err = db.Pool().Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id SERIAL PRIMARY KEY, name TEXT NOT NULL)", tableName))
	require.NoError(t, err)
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName))
	}()

	// Test commit: insert a row inside a transaction that succeeds.
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", tableName), "committed-row")
		if err != nil {
			return err
		}

		// Verify the row is visible inside the transaction.
		var count int
		err = tx.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = $1", tableName), "committed-row").Scan(&count)
		if err != nil {
			return err
		}
		assert.Equal(t, 1, count, "row should be visible inside the transaction")
		return nil
	})
	require.NoError(t, err)

	// Verify the row is persisted after commit.
	var count int
	err = db.Pool().QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = $1", tableName), "committed-row").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "committed row should be persisted")

	// Test rollback: insert a row inside a transaction that returns an error.
	err = db.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (name) VALUES ($1)", tableName), "rolled-back-row")
		if err != nil {
			return err
		}
		return fmt.Errorf("intentional error to trigger rollback")
	})
	require.Error(t, err, "WithTx should return an error when fn returns an error")

	// Verify the row was rolled back.
	err = db.Pool().QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE name = $1", tableName), "rolled-back-row").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "rolled-back row should not be persisted")
}

// TestPostgres_ConcurrentAccess runs 10 concurrent goroutines that each insert
// a row into a PostgreSQL table, then verifies all 10 rows exist after completion.
func TestPostgres_ConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := postgres.New(
		postgres.WithDSN(testDSN),
		postgres.WithMaxConns(10),
	)
	require.NoError(t, err)
	require.NoError(t, db.Start(ctx))
	defer func() { require.NoError(t, db.Stop(ctx)) }()

	tableName := fmt.Sprintf("test_concurrent_%d", time.Now().UnixNano())

	// Create test table.
	_, err = db.Pool().Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id SERIAL PRIMARY KEY, worker INT NOT NULL)", tableName))
	require.NoError(t, err)
	defer func() {
		_, _ = db.Pool().Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName))
	}()

	const numWorkers = 10
	var wg sync.WaitGroup
	errs := make([]error, numWorkers)

	for i := range numWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			_, errs[workerID] = db.Pool().Exec(ctx,
				fmt.Sprintf("INSERT INTO %s (worker) VALUES ($1)", tableName), workerID)
		}(i)
	}
	wg.Wait()

	// Verify all inserts succeeded.
	for i, err := range errs {
		assert.NoErrorf(t, err, "worker %d should not have errored", i)
	}

	// Verify all 10 rows exist.
	var count int
	err = db.Pool().QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, numWorkers, count, "all %d rows should be present", numWorkers)
}
