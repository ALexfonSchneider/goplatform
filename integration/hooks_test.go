//go:build integration

package integration

import (
	"context"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
	"github.com/ALexfonSchneider/goplatform/pkg/server"
)

// TestHooks_AllFourTypes verifies that all four hook types are called in the
// correct order: BeforeStart (per component), AfterStart (once after all),
// BeforeStop (once before shutdown), AfterStop (once after shutdown).
func TestHooks_AllFourTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	var (
		mu     sync.Mutex
		events []string
	)

	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	srv, err := server.New(server.WithAddr(":0"))
	require.NoError(t, err)

	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	require.NoError(t, app.Register("server", srv))

	// Register all 4 hook types.
	app.OnBeforeStart(func(_ context.Context, name string) error {
		record("before-start:" + name)
		return nil
	})
	app.OnAfterStart(func(_ context.Context) {
		record("after-start")
	})
	app.OnBeforeStop(func(_ context.Context) {
		record("before-stop")
	})
	app.OnAfterStop(func(_ context.Context, err error) {
		if err == nil {
			record("after-stop:ok")
		} else {
			record("after-stop:error")
		}
	})

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(context.Background())
	}()

	time.Sleep(500 * time.Millisecond)

	// Send SIGTERM.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify the order of events.
	require.Len(t, events, 4, "all 4 hooks should have been called")
	assert.Equal(t, "before-start:server", events[0])
	assert.Equal(t, "after-start", events[1])
	assert.Equal(t, "before-stop", events[2])
	assert.Equal(t, "after-stop:ok", events[3])
}

// TestHooks_MultipleComponents verifies that BeforeStart is called for each
// component in registration order, and components start/stop in correct order.
func TestHooks_MultipleComponents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	var (
		mu     sync.Mutex
		events []string
	)

	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}

	srv1, err := server.New(server.WithAddr(":0"))
	require.NoError(t, err)
	srv2, err := server.New(server.WithAddr(":0"))
	require.NoError(t, err)

	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	require.NoError(t, app.Register("server-1", srv1))
	require.NoError(t, app.Register("server-2", srv2))

	app.OnBeforeStart(func(_ context.Context, name string) error {
		record("before-start:" + name)
		return nil
	})

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(context.Background())
	}()

	time.Sleep(500 * time.Millisecond)

	// Verify both servers are listening.
	assert.NotEmpty(t, srv1.Addr(), "server-1 should be listening")
	assert.NotEmpty(t, srv2.Addr(), "server-2 should be listening")

	// Send SIGTERM.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}

	mu.Lock()
	defer mu.Unlock()

	// BeforeStart should be called for each component in registration order.
	require.Len(t, events, 2)
	assert.Equal(t, "before-start:server-1", events[0])
	assert.Equal(t, "before-start:server-2", events[1])
}
