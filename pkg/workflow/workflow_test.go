package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

func TestNewClient_Options(t *testing.T) {
	c, err := NewClient(
		WithHostPort("temporal.example.com:7233"),
		WithNamespace("my-namespace"),
		WithClientLogger(platform.NopLogger()),
	)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "temporal.example.com:7233", c.hostPort)
	assert.Equal(t, "my-namespace", c.namespace)
	assert.NotNil(t, c.logger)
}

func TestNewClient_Defaults(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "localhost:7233", c.hostPort)
	assert.Equal(t, "default", c.namespace)
	assert.NotNil(t, c.logger)
}

func TestNewWorker_Config(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	wf1 := func() {}
	wf2 := func() {}
	act1 := func() {}

	w, err := NewWorker(c, WorkerConfig{
		TaskQueue:  "test-queue",
		Workflows:  []any{wf1, wf2},
		Activities: []any{act1},
	})
	require.NoError(t, err)
	require.NotNil(t, w)

	assert.Equal(t, "test-queue", w.taskQueue)
	assert.Len(t, w.workflows, 2)
	assert.Len(t, w.activities, 1)
}

func TestNewWorker_NoTaskQueue(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	_, err = NewWorker(c, WorkerConfig{
		TaskQueue: "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task queue must not be empty")
}

func TestNewWorker_NilClient(t *testing.T) {
	_, err := NewWorker(nil, WorkerConfig{
		TaskQueue: "test-queue",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client must not be nil")
}

func TestWorkflowOptions(t *testing.T) {
	opts := WorkflowOptions{
		ID:        "order-123",
		TaskQueue: "orders",
	}

	assert.Equal(t, "order-123", opts.ID)
	assert.Equal(t, "orders", opts.TaskQueue)
}

func TestClient_StopBeforeStart(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	// Stop before Start should return nil without error.
	err = c.Stop(context.Background())
	assert.NoError(t, err)
}

func TestClient_TemporalClientBeforeStart(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	// TemporalClient returns nil before Start.
	assert.Nil(t, c.TemporalClient())
}

func TestClient_ExecuteWorkflowBeforeStart(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	_, err = c.ExecuteWorkflow(context.Background(), WorkflowOptions{
		ID:        "test-wf",
		TaskQueue: "test-queue",
	}, "SomeWorkflow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not started")
}

func TestClient_SignalWorkflowBeforeStart(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	err = c.SignalWorkflow(context.Background(), "wf-123", "my-signal", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not started")
}

func TestWorker_StopBeforeStart(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	w, err := NewWorker(c, WorkerConfig{
		TaskQueue: "test-queue",
	})
	require.NoError(t, err)

	// Stop before Start should return nil without error.
	err = w.Stop(context.Background())
	assert.NoError(t, err)
}

func TestClient_ImplementsComponent(t *testing.T) {
	var client interface{} = &Client{}
	_, ok := client.(platform.Component)
	assert.True(t, ok, "Client should implement platform.Component")
}

func TestWorker_ImplementsComponent(t *testing.T) {
	c, err := NewClient()
	require.NoError(t, err)

	w, err := NewWorker(c, WorkerConfig{TaskQueue: "q"})
	require.NoError(t, err)

	var worker interface{} = w
	_, ok := worker.(platform.Component)
	assert.True(t, ok, "Worker should implement platform.Component")
}
