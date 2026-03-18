// Package main is the entry point for the goplatform CLI tool.
package main

import "os"

func main() {
	cmd := rootCmd()
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
