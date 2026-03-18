package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

// runCmd creates the "run" subcommand that starts the development server
// with automatic hot reload. It watches .go files for changes, rebuilds
// the project, and restarts the process.
func runCmd() *cobra.Command {
	var (
		configFile string
		addr       string
		noReload   bool
		watchDirs  []string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the development server",
		Long: `Run the development server with hot reload support.

Watches .go files for changes, rebuilds the project, and restarts
the process automatically. Use --no-reload to disable hot reload.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDev(cmd.Context(), cmd, runConfig{
				configFile: configFile,
				addr:       addr,
				noReload:   noReload,
				watchDirs:  watchDirs,
			})
		},
	}

	cmd.Flags().StringVarP(&configFile, "config", "c", "config.yaml", "config file path")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "server address")
	cmd.Flags().BoolVar(&noReload, "no-reload", false, "disable hot reload")
	cmd.Flags().StringSliceVarP(&watchDirs, "watch", "w", []string{"."}, "directories to watch for changes")

	return cmd
}

// runConfig holds the configuration for the dev server runner.
type runConfig struct {
	configFile string
	addr       string
	noReload   bool
	watchDirs  []string
}

// runDev starts the development server and optionally watches for file changes.
func runDev(ctx context.Context, cmd *cobra.Command, cfg runConfig) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	out := cmd.OutOrStdout()

	r := &runner{
		out: out,
	}

	// Initial build and start.
	if err := r.restart(out); err != nil {
		return err
	}

	if cfg.noReload {
		_, _ = fmt.Fprintln(out, "hot reload disabled, waiting for signal...")
		<-ctx.Done()
		r.stop()
		return nil
	}

	// Set up file watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		r.stop()
		return fmt.Errorf("run: create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	for _, dir := range cfg.watchDirs {
		if err := addRecursive(watcher, dir); err != nil {
			r.stop()
			return fmt.Errorf("run: watch %q: %w", dir, err)
		}
	}

	_, _ = fmt.Fprintf(out, "watching for changes in %s\n", strings.Join(cfg.watchDirs, ", "))

	// Debounce timer to avoid rebuilding on every keystroke.
	var debounce *time.Timer

	for {
		select {
		case <-ctx.Done():
			r.stop()
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				r.stop()
				return nil
			}
			// Only react to .go file writes.
			if !isGoFile(event.Name) {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			// Debounce: wait 500ms of quiet before rebuilding.
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				_, _ = fmt.Fprintf(out, "\nfile changed: %s\n", event.Name)
				if err := r.restart(out); err != nil {
					_, _ = fmt.Fprintf(out, "build error: %v\n", err)
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				r.stop()
				return nil
			}
			_, _ = fmt.Fprintf(out, "watcher error: %v\n", err)
		}
	}
}

// runner manages the child process lifecycle (build + run).
type runner struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	out interface{ Write([]byte) (int, error) }
}

// restart kills the current process (if any), rebuilds, and starts a new one.
func (r *runner) restart(out interface{ Write([]byte) (int, error) }) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Kill previous process.
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
		// Give it a moment to shut down gracefully.
		done := make(chan error, 1)
		go func() { done <- r.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = r.cmd.Process.Kill()
			<-done
		}
	}

	_, _ = fmt.Fprintln(out, "building...")

	// Build.
	build := exec.Command("go", "build", "-o", "./tmp/main", "./cmd/...")
	build.Stdout = out
	build.Stderr = out
	if err := build.Run(); err != nil {
		return fmt.Errorf("run: build failed: %w", err)
	}

	_, _ = fmt.Fprintln(out, "starting...")

	// Start the new process.
	r.cmd = exec.Command("./tmp/main")
	r.cmd.Stdout = out
	r.cmd.Stderr = out
	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("run: start failed: %w", err)
	}

	_, _ = fmt.Fprintf(out, "process started (pid %d)\n", r.cmd.Process.Pid)
	return nil
}

// stop kills the current process if running.
func (r *runner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- r.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = r.cmd.Process.Kill()
			<-done
		}
		r.cmd = nil
	}
}

// addRecursive adds a directory and all its subdirectories to the watcher.
func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		// Skip hidden dirs, vendor, node_modules, tmp.
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") || base == "vendor" || base == "node_modules" || base == "tmp" {
			return filepath.SkipDir
		}
		return w.Add(path)
	})
}

// isGoFile checks if the file has a .go extension.
func isGoFile(name string) bool {
	return strings.HasSuffix(name, ".go")
}
