// Package workflow provides Temporal SDK wrappers that implement
// platform.Component for managed lifecycle integration.
//
// The package exposes two components:
//
//   - Client: wraps a Temporal SDK client, providing workflow execution
//     and signaling capabilities with automatic connection management.
//   - Worker: wraps a Temporal SDK worker, polling a task queue and
//     dispatching workflow and activity executions.
//
// Both Client and Worker implement platform.Component, so they can be
// registered with a platform.App for coordinated startup and shutdown.
//
// Usage:
//
//	c, _ := workflow.NewClient(
//	    workflow.WithHostPort("localhost:7233"),
//	    workflow.WithNamespace("my-namespace"),
//	)
//	app.Register("workflow-client", c)
//
//	w, _ := workflow.NewWorker(c, workflow.WorkerConfig{
//	    TaskQueue:  "my-queue",
//	    Workflows:  []any{MyWorkflow},
//	    Activities: []any{MyActivity},
//	})
//	app.Register("workflow-worker", w)
package workflow

import "context"

// WorkflowOptions configures a workflow execution request passed to
// Client.ExecuteWorkflow. It maps to a subset of the Temporal SDK's
// StartWorkflowOptions.
type WorkflowOptions struct {
	// ID is the business-level identifier for the workflow execution.
	// If empty, the Temporal server generates a random ID.
	ID string

	// TaskQueue is the Temporal task queue that a Worker must be polling
	// for the workflow to be dispatched. This field is required.
	TaskQueue string
}

// WorkflowRun represents a handle to a running or completed workflow
// execution. It allows callers to retrieve the workflow and run IDs, and
// to block until the workflow completes and obtain its result.
type WorkflowRun interface {
	// GetID returns the workflow ID of the execution.
	GetID() string

	// GetRunID returns the run ID of the execution. A workflow ID can
	// have multiple runs; the run ID uniquely identifies one of them.
	GetRunID() string

	// Get blocks until the workflow completes and decodes the result
	// into the value pointed to by valuePtr. If the workflow failed,
	// Get returns the failure as an error.
	Get(ctx context.Context, valuePtr any) error
}
