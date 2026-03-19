package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5 database driver for migrate
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Migrate runs all up migrations from the given filesystem source. The source
// must be an fs.ReadDirFS containing migration files following the golang-migrate
// naming convention (e.g. 001_create_users.up.sql, 001_create_users.down.sql).
//
// Migrate uses the DSN configured on the DB to connect to the database. It does
// not use the connection pool — golang-migrate manages its own connections.
//
// If all migrations have already been applied, Migrate returns nil (no error).
func (db *DB) Migrate(ctx context.Context, source fs.ReadDirFS) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("postgres: migrate up: %w", err)
	}

	m, err := db.newMigrate(source)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("postgres: migrate up: %w", err)
	}

	return nil
}

// MigrateDown rolls back all migrations from the given filesystem source. The
// source must be an fs.ReadDirFS containing migration files following the
// golang-migrate naming convention.
//
// MigrateDown uses the DSN configured on the DB to connect to the database.
// It does not use the connection pool — golang-migrate manages its own connections.
//
// If no migrations have been applied, MigrateDown returns nil (no error).
func (db *DB) MigrateDown(ctx context.Context, source fs.ReadDirFS) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("postgres: migrate down: %w", err)
	}

	m, err := db.newMigrate(source)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("postgres: migrate down: %w", err)
	}

	return nil
}

// closeMigrate closes the migrate instance, discarding any errors. This is used
// with defer to satisfy errcheck while acknowledging that close errors during
// cleanup are not actionable.
func closeMigrate(m *migrate.Migrate) {
	_, _ = m.Close()
}

// newMigrate creates a new migrate.Migrate instance using the DB's DSN and the
// given filesystem source. It converts the DSN from postgres:// to pgx5:// scheme
// as required by the pgx5 database driver for golang-migrate.
func (db *DB) newMigrate(source fs.ReadDirFS) (*migrate.Migrate, error) {
	d, err := iofs.New(source, ".")
	if err != nil {
		return nil, fmt.Errorf("postgres: create migration source: %w", err)
	}

	dsn := db.dsn
	// The pgx5 driver expects a pgx5:// scheme instead of postgres://.
	if len(dsn) >= 11 && dsn[:11] == "postgres://" {
		dsn = "pgx5://" + dsn[11:]
	} else if len(dsn) >= 14 && dsn[:14] == "postgresql://" {
		dsn = "pgx5://" + dsn[14:]
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: create migrator: %w", err)
	}

	return m, nil
}
