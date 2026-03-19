//go:build integration

package integration

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/postgres"
)

// TestPostgres_MigrateUpDown creates an in-memory migration source, applies
// migrations to a real PostgreSQL instance, verifies the schema was created,
// then rolls back and verifies the schema was removed.
func TestPostgres_MigrateUpDown(t *testing.T) {
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

	// Create an in-memory migration source using testing/fstest.
	migrations := fstest.MapFS{
		"001_create_integ_migrate.up.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE integ_migrate_test (id SERIAL PRIMARY KEY, name TEXT NOT NULL);"),
		},
		"001_create_integ_migrate.down.sql": &fstest.MapFile{
			Data: []byte("DROP TABLE IF EXISTS integ_migrate_test;"),
		},
	}

	// Migrate up.
	err = db.Migrate(ctx, migrations)
	require.NoError(t, err)

	// Verify table exists by inserting a row.
	_, err = db.Pool().Exec(ctx, "INSERT INTO integ_migrate_test (name) VALUES ($1)", "migration-test")
	require.NoError(t, err, "table should exist after migrate up")

	// Verify row was inserted.
	var count int
	err = db.Pool().QueryRow(ctx, "SELECT COUNT(*) FROM integ_migrate_test").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Migrate down.
	err = db.MigrateDown(ctx, migrations)
	require.NoError(t, err)

	// Verify table no longer exists.
	_, err = db.Pool().Exec(ctx, "SELECT 1 FROM integ_migrate_test LIMIT 1")
	require.Error(t, err, "table should not exist after migrate down")
}

// TestPostgres_MigrateIdempotent verifies that running Migrate twice does not
// cause errors (idempotent).
func TestPostgres_MigrateIdempotent(t *testing.T) {
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

	migrations := fstest.MapFS{
		"001_create_integ_idempotent.up.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE integ_idempotent_test (id SERIAL PRIMARY KEY);"),
		},
		"001_create_integ_idempotent.down.sql": &fstest.MapFile{
			Data: []byte("DROP TABLE IF EXISTS integ_idempotent_test;"),
		},
	}

	// First migration.
	err = db.Migrate(ctx, migrations)
	require.NoError(t, err)

	// Second migration should be a no-op (no error).
	err = db.Migrate(ctx, migrations)
	require.NoError(t, err)

	// Clean up.
	err = db.MigrateDown(ctx, migrations)
	require.NoError(t, err)
}
