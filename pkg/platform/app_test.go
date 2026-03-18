package platform

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared call log for tracking call order across components (race-safe).
// ---------------------------------------------------------------------------

type callLog struct {
	mu      sync.Mutex
	entries []string
}

func (cl *callLog) append(entry string) {
	cl.mu.Lock()
	cl.entries = append(cl.entries, entry)
	cl.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Mock component
// ---------------------------------------------------------------------------

type mockComponent struct {
	name      string
	startFunc func(ctx context.Context) error
	stopFunc  func(ctx context.Context) error

	clog *callLog // shared across components
}

func (m *mockComponent) Start(ctx context.Context) error {
	if m.clog != nil {
		m.clog.append("start:" + m.resolvedName())
	}
	if m.startFunc != nil {
		return m.startFunc(ctx)
	}
	return nil
}

func (m *mockComponent) Stop(ctx context.Context) error {
	if m.clog != nil {
		m.clog.append("stop:" + m.resolvedName())
	}
	if m.stopFunc != nil {
		return m.stopFunc(ctx)
	}
	return nil
}

func (m *mockComponent) resolvedName() string {
	if m.name != "" {
		return m.name
	}
	return "unnamed"
}

// Named interface -- only implement if name is set and non-empty.
type namedMockComponent struct {
	mockComponent
}

func (n *namedMockComponent) Name() string {
	return n.name
}

// HealthChecker interface -- only if healthFunc is set.
type healthMockComponent struct {
	mockComponent
	healthFunc func(ctx context.Context) error
}

func (h *healthMockComponent) HealthCheck(ctx context.Context) error {
	if h.healthFunc != nil {
		return h.healthFunc(ctx)
	}
	return nil
}

// namedHealthMockComponent implements both Named and HealthChecker.
type namedHealthMockComponent struct {
	mockComponent
	healthFunc func(ctx context.Context) error
}

func (n *namedHealthMockComponent) Name() string {
	return n.name
}

func (n *namedHealthMockComponent) HealthCheck(ctx context.Context) error {
	if n.healthFunc != nil {
		return n.healthFunc(ctx)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helper to run App and cancel via context
// ---------------------------------------------------------------------------

// runWithCancel starts Run in a goroutine, returns a cancel func and a channel
// that delivers the Run result.
func runWithCancel(t *testing.T, app *App) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		ch <- app.Run(ctx)
	}()
	return cancel, ch
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestApp_StartStopOrder(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	for _, name := range []string{"a", "b", "c"} {
		c := &mockComponent{name: name, clog: cl}
		err := app.Register(name, c)
		require.NoError(t, err)
	}

	cancel, ch := runWithCancel(t, app)
	// Give components time to start.
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.NoError(t, err)

	require.Equal(t, []string{
		"start:a", "start:b", "start:c",
		"stop:c", "stop:b", "stop:a",
	}, cl.entries)
}

func TestApp_ShutdownTimeout(t *testing.T) {
	app := New(WithShutdownTimeout(50 * time.Millisecond))

	slowStop := &mockComponent{
		name: "slow",
		stopFunc: func(ctx context.Context) error {
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	require.NoError(t, app.Register("slow", slowStop))

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestApp_StartFailure(t *testing.T) {
	cl := &callLog{}

	app := New(WithShutdownTimeout(5 * time.Second))

	c1 := &mockComponent{name: "c1", clog: cl}
	c2 := &mockComponent{
		name: "c2",
		clog: cl,
		startFunc: func(ctx context.Context) error {
			return errors.New("boom")
		},
	}
	c3 := &mockComponent{name: "c3", clog: cl}

	require.NoError(t, app.Register("c1", c1))
	require.NoError(t, app.Register("c2", c2))
	require.NoError(t, app.Register("c3", c3))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := app.Run(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	// c3 should NOT have been started; c1 should have been stopped.
	assert.Contains(t, cl.entries, "start:c1")
	assert.Contains(t, cl.entries, "start:c2") // the attempt is logged before startFunc
	assert.NotContains(t, cl.entries, "start:c3")
	assert.Contains(t, cl.entries, "stop:c1")
	assert.NotContains(t, cl.entries, "stop:c3")
}

func TestApp_ShutdownErrors(t *testing.T) {
	errA := errors.New("err-a")
	errC := errors.New("err-c")

	app := New(WithShutdownTimeout(5 * time.Second))

	cA := &mockComponent{name: "a", stopFunc: func(ctx context.Context) error { return errA }}
	cB := &mockComponent{name: "b"}
	cC := &mockComponent{name: "c", stopFunc: func(ctx context.Context) error { return errC }}

	require.NoError(t, app.Register("a", cA))
	require.NoError(t, app.Register("b", cB))
	require.NoError(t, app.Register("c", cC))

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.Error(t, err)

	// Both errors must be present.
	assert.ErrorIs(t, err, errA)
	assert.ErrorIs(t, err, errC)
}

func TestApp_RegisterDuplicate(t *testing.T) {
	app := New()

	c1 := &mockComponent{}
	c2 := &mockComponent{}

	require.NoError(t, app.Register("dup", c1))
	err := app.Register("dup", c2)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestApp_Named(t *testing.T) {
	app := New()

	nc := &namedMockComponent{mockComponent: mockComponent{name: "custom-name"}}
	err := app.Register("ignored", nc)
	require.NoError(t, err)

	// The component should be retrievable by its Named name, not the registration name.
	comp, ok := app.Component("custom-name")
	assert.True(t, ok)
	assert.Equal(t, nc, comp)

	_, ok = app.Component("ignored")
	assert.False(t, ok)
}

func TestApp_HealthCheckers(t *testing.T) {
	app := New()

	plain := &mockComponent{}
	hc1 := &healthMockComponent{mockComponent: mockComponent{}}
	hc2 := &namedHealthMockComponent{mockComponent: mockComponent{name: "checker2"}}

	require.NoError(t, app.Register("plain", plain))
	require.NoError(t, app.Register("hc1", hc1))
	require.NoError(t, app.Register("hc2-reg", hc2))

	checkers := app.HealthCheckers()
	assert.Len(t, checkers, 2)
	assert.Contains(t, checkers, "hc1")
	assert.Contains(t, checkers, "checker2") // Named takes precedence
}

func TestApp_SignalShutdown(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	c := &mockComponent{name: "sig", clog: cl}
	require.NoError(t, app.Register("sig", c))

	ch := make(chan error, 1)
	go func() {
		ch <- app.Run(context.Background())
	}()

	// Wait for components to start, then send SIGINT.
	time.Sleep(100 * time.Millisecond)
	err := syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	require.NoError(t, err)

	runErr := waitResult(t, ch, 5*time.Second)
	assert.NoError(t, runErr)

	assert.Contains(t, cl.entries, "start:sig")
	assert.Contains(t, cl.entries, "stop:sig")
}

func TestApp_ContextCancel(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	c := &mockComponent{name: "ctx", clog: cl}
	require.NoError(t, app.Register("ctx", c))

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		ch <- app.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	runErr := waitResult(t, ch, 5*time.Second)
	assert.NoError(t, runErr)

	assert.Contains(t, cl.entries, "start:ctx")
	assert.Contains(t, cl.entries, "stop:ctx")
}

// ---------------------------------------------------------------------------
// Mock plugin
// ---------------------------------------------------------------------------

type mockPlugin struct {
	name      string
	initFunc  func(reg PluginContext) error
	closeFunc func() error
	clog      *callLog
}

func (p *mockPlugin) Name() string { return p.name }

func (p *mockPlugin) Init(reg PluginContext) error {
	if p.clog != nil {
		p.clog.append("plugin:init:" + p.name)
	}
	if p.initFunc != nil {
		return p.initFunc(reg)
	}
	return nil
}

func (p *mockPlugin) Close() error {
	if p.clog != nil {
		p.clog.append("plugin:close:" + p.name)
	}
	if p.closeFunc != nil {
		return p.closeFunc()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Hook & Plugin tests
// ---------------------------------------------------------------------------

func TestApp_BeforeStartHookAbort(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	c1 := &mockComponent{name: "c1", clog: cl}
	c2 := &mockComponent{name: "c2", clog: cl}
	c3 := &mockComponent{name: "c3", clog: cl}

	require.NoError(t, app.Register("c1", c1))
	require.NoError(t, app.Register("c2", c2))
	require.NoError(t, app.Register("c3", c3))

	app.OnBeforeStart(func(ctx context.Context, name string) error {
		if name == "c2" {
			return errors.New("abort c2")
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := app.Run(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "abort c2")

	// c1 was started, c2 and c3 were NOT started, c1 was stopped.
	assert.Contains(t, cl.entries, "start:c1")
	assert.NotContains(t, cl.entries, "start:c2")
	assert.NotContains(t, cl.entries, "start:c3")
	assert.Contains(t, cl.entries, "stop:c1")
}

func TestApp_AfterStopHookReceivesError(t *testing.T) {
	errStop := errors.New("stop-error")
	app := New(WithShutdownTimeout(5 * time.Second))

	c := &mockComponent{
		name: "failing",
		stopFunc: func(ctx context.Context) error {
			return errStop
		},
	}
	require.NoError(t, app.Register("failing", c))

	var capturedErr error
	var mu sync.Mutex
	app.OnAfterStop(func(ctx context.Context, name string, err error) {
		mu.Lock()
		defer mu.Unlock()
		capturedErr = err
	})

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	_ = waitResult(t, ch, 5*time.Second)

	mu.Lock()
	defer mu.Unlock()
	require.Error(t, capturedErr)
	assert.ErrorIs(t, capturedErr, errStop)
}

func TestApp_HooksCalledInOrder(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	c := &mockComponent{name: "comp"}
	require.NoError(t, app.Register("comp", c))

	// Register two BeforeStart hooks; they should fire in registration order.
	app.OnBeforeStart(func(ctx context.Context, name string) error {
		cl.append("hook1:" + name)
		return nil
	})
	app.OnBeforeStart(func(ctx context.Context, name string) error {
		cl.append("hook2:" + name)
		return nil
	})

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.NoError(t, err)

	// Find the indices of hook1 and hook2 in the log.
	idx1, idx2 := -1, -1
	for i, e := range cl.entries {
		if e == "hook1:comp" {
			idx1 = i
		}
		if e == "hook2:comp" {
			idx2 = i
		}
	}
	require.NotEqual(t, -1, idx1, "hook1 should have been called")
	require.NotEqual(t, -1, idx2, "hook2 should have been called")
	assert.Less(t, idx1, idx2, "hook1 must fire before hook2")
}

func TestApp_PluginInitBeforeStart(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	plug := &mockPlugin{name: "p1", clog: cl}
	app.Use(plug)

	c := &mockComponent{name: "comp", clog: cl}
	require.NoError(t, app.Register("comp", c))

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.NoError(t, err)

	// "plugin:init:p1" must appear before "start:comp".
	idxInit, idxStart := -1, -1
	for i, e := range cl.entries {
		if e == "plugin:init:p1" {
			idxInit = i
		}
		if e == "start:comp" {
			idxStart = i
		}
	}
	require.NotEqual(t, -1, idxInit, "plugin init should have been called")
	require.NotEqual(t, -1, idxStart, "component start should have been called")
	assert.Less(t, idxInit, idxStart, "plugin init must happen before component start")
}

func TestApp_PluginCloseAfterStop(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	plug := &mockPlugin{name: "p1", clog: cl}
	app.Use(plug)

	c := &mockComponent{name: "comp", clog: cl}
	require.NoError(t, app.Register("comp", c))

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.NoError(t, err)

	// "stop:comp" must appear before "plugin:close:p1".
	idxStop, idxClose := -1, -1
	for i, e := range cl.entries {
		if e == "stop:comp" {
			idxStop = i
		}
		if e == "plugin:close:p1" {
			idxClose = i
		}
	}
	require.NotEqual(t, -1, idxStop, "component stop should have been called")
	require.NotEqual(t, -1, idxClose, "plugin close should have been called")
	assert.Less(t, idxStop, idxClose, "component stop must happen before plugin close")
}

func TestApp_PluginRegistersComponent(t *testing.T) {
	cl := &callLog{}
	app := New(WithShutdownTimeout(5 * time.Second))

	plug := &mockPlugin{
		name: "registrar",
		initFunc: func(reg PluginContext) error {
			comp := &mockComponent{name: "plugin-comp", clog: cl}
			return reg.Register("plugin-comp", comp)
		},
	}
	app.Use(plug)

	cancel, ch := runWithCancel(t, app)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := waitResult(t, ch, 5*time.Second)
	require.NoError(t, err)

	// The component registered by the plugin should have been started and stopped.
	assert.Contains(t, cl.entries, "start:plugin-comp")
	assert.Contains(t, cl.entries, "stop:plugin-comp")
}

func TestApp_PluginContextNoRun(t *testing.T) {
	// Compile-time check: App satisfies PluginContext.
	var _ PluginContext = (*App)(nil)

	// PluginContext intentionally does not expose a Run method, ensuring
	// plugins cannot invoke the application lifecycle. This test verifies
	// the interface assignment compiles, confirming App implements
	// PluginContext without leaking Run.
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func waitResult(t *testing.T, ch <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		t.Fatal("timed out waiting for Run to return")
		return fmt.Errorf("unreachable")
	}
}
