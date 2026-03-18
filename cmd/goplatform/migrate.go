package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

	cmd.PersistentFlags().StringVar(&dsn, "dsn", "", "PostgreSQL DSN (default from env POSTGRES_DSN or config)")
	cmd.PersistentFlags().StringVar(&dir, "dir", "migrations", "migrations directory")

	cmd.AddCommand(migrateUpCmd(&dsn, &dir))
	cmd.AddCommand(migrateDownCmd(&dsn, &dir))
	cmd.AddCommand(migrateCreateCmd(&dir))

	return cmd
}

// migrateUpCmd creates the "migrate up" subcommand that runs all pending migrations.
func migrateUpCmd(dsn, dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Run all pending migrations",
		Long:  "Run all pending database migrations.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d := *dsn
			if d == "" {
				d = os.Getenv("POSTGRES_DSN")
			}
			_ = d // DSN will be used when real migration logic is implemented.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Running migrations from %s\n", *dir)
			return nil
		},
	}
}

// migrateDownCmd creates the "migrate down" subcommand that rolls back the last migration.
func migrateDownCmd(dsn, dir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Rollback last migration",
		Long:  "Rollback the last applied database migration.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d := *dsn
			if d == "" {
				d = os.Getenv("POSTGRES_DSN")
			}
			_ = d // DSN will be used when real migration logic is implemented.
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rolling back last migration from %s\n", *dir)
			return nil
		},
	}
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
