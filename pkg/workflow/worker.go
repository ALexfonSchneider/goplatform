package workflow

import (
	"fmt"
	"sync"

	"go.temporal.io/sdk/worker"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"

	"context"
)

// Compile-time check that Worker implements platform.Component.
var _ platform.Component = (*Worker)(nil)

// Worker wraps the Temporal SDK worker and implements platform.Component.
// It polls a Temporal task queue for workflow and activity tasks, dispatching
// them to registered handler functions.
//
// Worker is safe for concurrent use. All exported methods are synchronized
// via an internal mutex.
type Worker struct {
	mu sync.Mutex

	// Dependencies.
	client *Client

	// Configuration.
	taskQueue  string
	workflows  []any
	activities []any

	// Runtime state; nil until Start is called.
	temporalWorker worker.Worker
}

// WorkerConfig holds the configuration for creating a new Worker.
type WorkerConfig struct {
	// TaskQueue is the Temporal task queue name this Worker polls.
	// This field is required and must not be empty.
	TaskQueue string

	// Workflows is a list of workflow functions or structs to register
	// with the worker. Each element is passed to worker.RegisterWorkflow.
	Workflows []any

	// Activities is a list of activity functions or structs to register
	// with the worker. Each element is passed to worker.RegisterActivity.
	Activities []any
}

// NewWorker creates a new Worker bound to the given Client and configured
// according to cfg. The Worker is not running until Start is called.
//
// NewWorker returns an error if client is nil or if cfg.TaskQueue is empty.
func NewWorker(client *Client, cfg WorkerConfig) (*Worker, error) {
	if client == nil {
		return nil, fmt.Errorf("workflow: client must not be nil")
	}
	if cfg.TaskQueue == "" {
		return nil, fmt.Errorf("workflow: task queue must not be empty")
	}

	return &Worker{
		client:     client,
		taskQueue:  cfg.TaskQueue,
		workflows:  cfg.Workflows,
		activities: cfg.Activities,
	}, nil
}

// Start creates the underlying Temporal worker, registers all configured
// workflows and activities, and begins the polling loop. It implements
// platform.Component.
//
// The Client must be started before calling Worker.Start so that the
// underlying Temporal SDK client is available. Calling Start on an
// already-started Worker returns an error.
func (w *Worker) Start(_ context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.temporalWorker != nil {
		return fmt.Errorf("workflow: worker already started")
	}

	tc := w.client.TemporalClient()
	if tc == nil {
		return fmt.Errorf("workflow: client not started; start the client before the worker")
	}

	tw := worker.New(tc, w.taskQueue, worker.Options{})

	for _, wf := range w.workflows {
		tw.RegisterWorkflow(wf)
	}
	for _, act := range w.activities {
		tw.RegisterActivity(act)
	}

	if err := tw.Start(); err != nil {
		return fmt.Errorf("workflow: start worker: %w", err)
	}

	w.temporalWorker = tw
	return nil
}

// Stop gracefully shuts down the worker, waiting for in-flight workflow
// and activity tasks to complete. It implements platform.Component.
//
// If the worker does not stop within the deadline carried by ctx, Stop
// returns an error wrapping ctx.Err(). Stop is safe to call before Start
// or multiple times — subsequent calls are no-ops.
func (w *Worker) Stop(ctx context.Context) error {
	w.mu.Lock()
	tw := w.temporalWorker
	w.temporalWorker = nil
	w.mu.Unlock()

	if tw == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		tw.Stop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("workflow: worker stop timed out: %w", ctx.Err())
	}
}
