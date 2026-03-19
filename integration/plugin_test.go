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

// testPlugin is a mock plugin for integration tests that tracks lifecycle calls.
type testPlugin struct {
	name        string
	initCalled  bool
	closeCalled bool
	initFn      func(reg platform.PluginContext) error
}

func (p *testPlugin) Name() string { return p.name }

func (p *testPlugin) Init(reg platform.PluginContext) error {
	p.initCalled = true
	if p.initFn != nil {
		return p.initFn(reg)
	}
	return nil
}

func (p *testPlugin) Close() error {
	p.closeCalled = true
	return nil
}

// TestPlugin_Lifecycle verifies that plugins are initialized before components
// start and closed after components stop.
func TestPlugin_Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	plugin := &testPlugin{name: "test-plugin"}

	srv, err := server.New(server.WithAddr(":0"))
	require.NoError(t, err)

	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	app.Use(plugin)
	require.NoError(t, app.Register("server", srv))

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(context.Background())
	}()

	time.Sleep(500 * time.Millisecond)

	// Plugin should be initialized.
	assert.True(t, plugin.initCalled, "plugin Init should have been called")

	// Send SIGTERM to shut down.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}

	// Plugin should be closed.
	assert.True(t, plugin.closeCalled, "plugin Close should have been called")
}

// TestPlugin_RegistersComponent verifies that a plugin can register additional
// components during Init.
func TestPlugin_RegistersComponent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long test in short mode")
	}

	pluginSrv, err := server.New(server.WithAddr(":0"))
	require.NoError(t, err)

	plugin := &testPlugin{
		name: "component-plugin",
		initFn: func(reg platform.PluginContext) error {
			return reg.Register("plugin-server", pluginSrv)
		},
	}

	app := platform.New(
		platform.WithShutdownTimeout(5 * time.Second),
	)
	app.Use(plugin)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(context.Background())
	}()

	time.Sleep(500 * time.Millisecond)

	// The plugin-registered server should be started and listening.
	addr := pluginSrv.Addr()
	assert.NotEmpty(t, addr, "plugin-registered server should be listening")

	// Send SIGTERM to shut down.
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGTERM))

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for Run to return")
	}
}
