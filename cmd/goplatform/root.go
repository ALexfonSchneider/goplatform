package main

import "github.com/spf13/cobra"

// rootCmd creates and returns the root cobra command for the goplatform CLI.
func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "goplatform",
		Short:         "goplatform — microservice scaffolding and development tool",
		Long:          "goplatform — microservice scaffolding and development tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.AddCommand(initCmd())
	cmd.AddCommand(runCmd())
	cmd.AddCommand(migrateCmd())

	return cmd
}
