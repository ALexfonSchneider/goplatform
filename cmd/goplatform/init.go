package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ALexfonSchneider/goplatform/internal/scaffold"
	"github.com/spf13/cobra"
)

// initCmd creates the "init" subcommand that scaffolds a new service project.
func initCmd() *cobra.Command {
	var (
		postgres bool
		kafka    bool
		nats     bool
		redis    bool
		temporal bool
		connect  bool
		module   string
	)

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Initialize a new service project",
		Long:  "Initialize a new service project with optional component support.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			return runInit(cmd, name, module, postgres, kafka, nats, redis, temporal, connect)
		},
	}

	cmd.Flags().BoolVar(&postgres, "postgres", false, "include PostgreSQL support")
	cmd.Flags().BoolVar(&kafka, "kafka", false, "include Kafka broker")
	cmd.Flags().BoolVar(&nats, "nats", false, "include NATS broker")
	cmd.Flags().BoolVar(&redis, "redis", false, "include Redis")
	cmd.Flags().BoolVar(&temporal, "temporal", false, "include Temporal workflows")
	cmd.Flags().BoolVar(&connect, "connect", false, "include ConnectRPC")
	cmd.Flags().StringVarP(&module, "module", "m", "", "Go module path (default: github.com/<user>/<name>)")

	return cmd
}

// runInit performs the project scaffolding for the init command.
func runInit(cmd *cobra.Command, name, module string, postgres, kafka, nats, redis, temporal, connect bool) error {
	outputDir := name
	baseName := filepath.Base(name)

	if module == "" {
		user := os.Getenv("USER")
		if user == "" {
			user = "user"
		}
		module = fmt.Sprintf("github.com/%s/%s", user, baseName)
	}

	// Create the project directory.
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}

	// Create go.mod.
	goModPath := filepath.Join(outputDir, "go.mod")
	goModContent := fmt.Sprintf("module %s\n\ngo 1.25\n", module)
	if err := os.WriteFile(goModPath, []byte(goModContent), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}

	// Render scaffold templates.
	gen := scaffold.New()
	data := scaffold.Data{
		Name:     baseName,
		Module:   module,
		Postgres: postgres,
		Kafka:    kafka,
		NATS:     nats,
		Redis:    redis,
		Temporal: temporal,
		Connect:  connect,
	}

	if err := gen.Render(outputDir, data); err != nil {
		return fmt.Errorf("rendering templates: %w", err)
	}

	// Build component list for output.
	var components []string
	if postgres {
		components = append(components, "postgres")
	}
	if kafka {
		components = append(components, "kafka")
	}
	if nats {
		components = append(components, "nats")
	}
	if redis {
		components = append(components, "redis")
	}
	if temporal {
		components = append(components, "temporal")
	}
	if connect {
		components = append(components, "connect")
	}

	if len(components) > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created project %s with %s\n", name, strings.Join(components, ", "))
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created project %s\n", name)
	}

	return nil
}
