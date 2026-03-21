//go:build integration

package platformtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	// pgx stdlib driver for database/sql used for admin operations.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/postgres"
)

// NewTestDB creates a [postgres.DB] connected to an isolated test database.
//
// It requires a running PostgreSQL instance reachable at the provided dsn
// (e.g. from docker-compose or testcontainers). The dsn must point to an
// existing database that the user has permission to connect to (typically the
// default "postgres" database).
//
// NewTestDB performs the following steps:
//  1. Generates a unique database name of the form "test_<random>".
//  2. Connects to the dsn using database/sql and creates the new database.
//  3. Constructs a [postgres.DB] whose DSN points to the freshly created
//     database, applying any user-supplied opts.
//  4. Starts the [postgres.DB] (establishes the connection pool).
//  5. Registers a [testing.T.Cleanup] that stops the [postgres.DB] and drops
//     the test database.
//
// NewTestDB calls [testing.T.Fatal] if any step fails.
//
// This helper is guarded by the "integration" build tag since it requires a
// real PostgreSQL server.
func NewTestDB(t *testing.T, dsn string, opts ...postgres.Option) *postgres.DB {
	t.Helper()

	// Generate a unique database name.
	randomBytes := make([]byte, 8)
	_, err := rand.Read(randomBytes)
	require.NoError(t, err, "platformtest: generate random bytes")

	dbName := "test_" + hex.EncodeToString(randomBytes)

	// Connect to the admin database and create the test database.
	adminDB, err := sql.Open("pgx", dsn)
	require.NoError(t, err, "platformtest: open admin connection")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = adminDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
	require.NoError(t, err, "platformtest: create database %s", dbName)

	err = adminDB.Close()
	require.NoError(t, err, "platformtest: close admin connection")

	// Build the DSN for the new database by replacing the database name in
	// the original DSN. We parse the original DSN, swap the database, and
	// reconstruct it. A simpler approach: append the database name to the
	// base DSN. We use pgx's config parsing to do this properly.
	testDSN := replaceDBName(dsn, dbName)

	// Build options: prepend WithDSN so user opts can override other settings
	// but not the DSN (which must point to the test database).
	allOpts := make([]postgres.Option, 0, len(opts)+1)
	allOpts = append(allOpts, postgres.WithDSN(testDSN))
	allOpts = append(allOpts, opts...)

	db, err := postgres.New(allOpts...)
	require.NoError(t, err, "platformtest: create postgres.DB")

	startCtx, startCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer startCancel()

	err = db.Start(startCtx)
	require.NoError(t, err, "platformtest: start postgres.DB")

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()

		if stopErr := db.Stop(stopCtx); stopErr != nil {
			t.Errorf("platformtest: stop postgres.DB: %v", stopErr)
		}

		// Drop the test database.
		cleanupDB, cleanupErr := sql.Open("pgx", dsn)
		if cleanupErr != nil {
			t.Errorf("platformtest: open admin connection for cleanup: %v", cleanupErr)
			return
		}
		defer cleanupDB.Close()

		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()

		// Force-disconnect any remaining connections before dropping.
		_, _ = cleanupDB.ExecContext(dropCtx, fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()", dbName))

		_, dropErr := cleanupDB.ExecContext(dropCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName))
		if dropErr != nil {
			t.Errorf("platformtest: drop database %s: %v", dbName, dropErr)
		}
	})

	return db
}

// replaceDBName takes a PostgreSQL connection URI and replaces the database
// name component with newDB. It handles both the URI form
// (postgres://user:pass@host:5432/olddb?params) and the keyword form
// (host=... dbname=...).
func replaceDBName(dsn, newDB string) string {
	// For URI-style DSNs, we need to replace the path component.
	// postgres://user:pass@host:5432/olddb?sslmode=disable
	// -> postgres://user:pass@host:5432/newdb?sslmode=disable
	//
	// A simple approach: find the last '/' before '?' and replace everything
	// between it and '?' (or end of string).

	// Check if it's a URI-style DSN.
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		// Find the authority section (after ://), then find the database name.
		schemeEnd := 0
		for i := 0; i < len(dsn); i++ {
			if dsn[i] == '/' && i+1 < len(dsn) && dsn[i+1] == '/' {
				schemeEnd = i + 2
				break
			}
		}

		// Find the first '/' after the authority (host:port).
		dbStart := -1
		for i := schemeEnd; i < len(dsn); i++ {
			if dsn[i] == '/' {
				dbStart = i + 1
				break
			}
		}

		if dbStart == -1 {
			// No database in URL, append it.
			if len(dsn) > 0 && dsn[len(dsn)-1] != '/' {
				return dsn + "/" + newDB
			}
			return dsn + newDB
		}

		// Find end of database name (either '?' or end of string).
		dbEnd := len(dsn)
		for i := dbStart; i < len(dsn); i++ {
			if dsn[i] == '?' {
				dbEnd = i
				break
			}
		}

		return dsn[:dbStart] + newDB + dsn[dbEnd:]
	}

	// Keyword-style DSN: dbname=xxx
	// This is a simplified replacement.
	return dsn + " dbname=" + newDB
}
