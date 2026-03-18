//go:build integration

package integration

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// TestLifecycle_GracefulShutdown creates a full App with a Server component,
// starts Run in a goroutine, sends SIGTERM to the own process, and verifies
// graceful shutdown completes without error and all components are stopped.
func TestLifecycle_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	srv, err := server.New(
		server.WithAddr(":0"),
	)
	require.NoError(t, err)

	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	require.NoError(t, app.Register("server", srv))

	// Track lifecycle via hooks.
	var (
		beforeStopCalled bool
		afterStopCalled  bool
	)
	app.OnBeforeStop(func(_ context.Context, _ string) {
		beforeStopCalled = true
	})
	app.OnAfterStop(func(_ context.Context, _ string, _ error) {
		afterStopCalled = true
	})

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(context.Background())
	}()

	// Give the server time to start listening.
	time.Sleep(500 * time.Millisecond)

	// Verify the server is listening.
	addr := srv.Addr()
	assert.NotEmpty(t, addr, "server should be listening on a port")

	// Send SIGTERM to own process to trigger graceful shutdown.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	// Wait for Run to return.
	select {
	case err := <-runErr:
		assert.NoError(t, err, "Run should return nil on graceful shutdown")
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return after SIGTERM")
	}

	// Verify hooks were called.
	assert.True(t, beforeStopCalled, "BeforeStop hook should have been called")
	assert.True(t, afterStopCalled, "AfterStop hook should have been called")

	// Verify server has stopped (Addr returns empty after stop).
	assert.Empty(t, srv.Addr(), "server address should be empty after stop")
}
