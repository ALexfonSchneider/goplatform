package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/spf13/cobra"
)

// migrateCmd creates the "migrate" subcommand with up, down, and create subcommands.
func migrateCmd() *cobra.Command {
	var (
		dsn string
		dir string
	)

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		Long:  "Run database migrations including up, down, and create operations.",
	}

	cmd.PersistentFlags().StringVar(&dsn, "dsn", "", "PostgreSQL DSN (default from env POSTGRES_DSN)")
	cmd.PersistentFlags().StringVar(&dir, "dir", "migrations", "migrations directory")

	cmd.AddCommand(migrateUpCmd(&dsn, &dir))
	cmd.AddCommand(migrateDownCmd(&dsn, &dir))
	cmd.AddCommand(migrateCreateCmd(&dir))

	return cmd
}

// resolveDSN returns the DSN from the flag or POSTGRES_DSN env var.
// The DSN scheme is converted from postgres:// to pgx5:// for golang-migrate.
func resolveDSN(dsn string) (string, error) {
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_DSN")
	}
	if dsn == "" {
		return "", fmt.Errorf("DSN not set: use --dsn flag or POSTGRES_DSN env var")
	}
	// golang-migrate pgx driver requires pgx:// scheme.
	if strings.HasPrefix(dsn, "postgres://") {
		dsn = "pgx://" + strings.TrimPrefix(dsn, "postgres://")
	} else if strings.HasPrefix(dsn, "postgresql://") {
		dsn = "pgx://" + strings.TrimPrefix(dsn, "postgresql://")
	}
	return dsn, nil
}

// resolveSourceURL returns the file:// URL for the migrations directory.
func resolveSourceURL(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving migrations path: %w", err)
	}
	return "file://" + absDir, nil
}

// migrateUpCmd creates the "migrate up" subcommand that runs all pending migrations.
func migrateUpCmd(dsn, dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Run all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbURL, err := resolveDSN(*dsn)
			if err != nil {
				return err
			}
			sourceURL, err := resolveSourceURL(*dir)
			if err != nil {
				return err
			}

			m, err := migrate.New(sourceURL, dbURL)
			if err != nil {
				return fmt.Errorf("migrate: init: %w", err)
			}
			defer func() { _, _ = m.Close() }()

			if err := m.Up(); err != nil {
				if errors.Is(err, migrate.ErrNoChange) {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No new migrations to apply.")
					return nil
				}
				return fmt.Errorf("migrate: up: %w", err)
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Migrations applied successfully.")
			return nil
		},
	}
}

// migrateDownCmd creates the "migrate down" subcommand that rolls back the last migration.
func migrateDownCmd(dsn, dir *string) *cobra.Command {
	var steps int

	cmd := &cobra.Command{
		Use:   "down",
		Short: "Rollback migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dbURL, err := resolveDSN(*dsn)
			if err != nil {
				return err
			}
			sourceURL, err := resolveSourceURL(*dir)
			if err != nil {
				return err
			}

			m, err := migrate.New(sourceURL, dbURL)
			if err != nil {
				return fmt.Errorf("migrate: init: %w", err)
			}
			defer func() { _, _ = m.Close() }()

			if err := m.Steps(-steps); err != nil {
				if errors.Is(err, migrate.ErrNoChange) {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No migrations to rollback.")
					return nil
				}
				return fmt.Errorf("migrate: down: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rolled back %d migration(s).\n", steps)
			return nil
		},
	}

	cmd.Flags().IntVarP(&steps, "steps", "n", 1, "number of migrations to rollback")

	return cmd
}

// migrateCreateCmd creates the "migrate create" subcommand that generates migration files.
func migrateCreateCmd(dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create new migration files",
		Long:  "Create new up and down migration SQL files with a timestamped prefix.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return runMigrateCreate(cmd, *dir, name)
		},
	}
}

// runMigrateCreate generates timestamped migration files in the specified directory.
func runMigrateCreate(cmd *cobra.Command, dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating migrations directory: %w", err)
	}

	timestamp := time.Now().Format("20060102150405")
	upFile := filepath.Join(dir, fmt.Sprintf("%s_%s.up.sql", timestamp, name))
	downFile := filepath.Join(dir, fmt.Sprintf("%s_%s.down.sql", timestamp, name))

	if err := os.WriteFile(upFile, []byte(""), 0o644); err != nil {
		return fmt.Errorf("creating up migration: %w", err)
	}
	if err := os.WriteFile(downFile, []byte(""), 0o644); err != nil {
		return fmt.Errorf("creating down migration: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created migration files:\n  %s\n  %s\n", upFile, downFile)
	return nil
}
