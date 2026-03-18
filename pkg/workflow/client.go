package workflow

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"

	"github.com/ALexfonSchneider/goplatform/pkg/platform"
)

// Compile-time check that Client implements platform.Component.
var _ platform.Component = (*Client)(nil)

// Client wraps the Temporal SDK client and implements platform.Component.
// It provides a managed lifecycle for Temporal connections, allowing
// callers to start workflow executions and send signals to running
// workflows.
//
// Client is safe for concurrent use. All exported methods are
// synchronized via an internal mutex except TemporalClient, which
// performs a single atomic read.
type Client struct {
	mu sync.Mutex

	// Configuration set via ClientOption functions.
	hostPort       string
	namespace      string
	logger         platform.Logger
	tracerProvider trace.TracerProvider

	// Runtime state; nil until Start is called.
	temporalClient client.Client
}

// ClientOption configures a Client instance. Options are applied in order
// during NewClient, with later options overriding earlier ones.
type ClientOption func(*Client)

// WithHostPort sets the Temporal server address in "host:port" format.
// The default address is "localhost:7233".
func WithHostPort(hostPort string) ClientOption {
	return func(c *Client) {
		c.hostPort = hostPort
	}
}

// WithNamespace sets the Temporal namespace the Client connects to.
// The default namespace is "default".
func WithNamespace(ns string) ClientOption {
	return func(c *Client) {
		c.namespace = ns
	}
}

// WithClientLogger sets the structured logger used by Client for
// connection lifecycle events and internal diagnostics.
func WithClientLogger(l platform.Logger) ClientOption {
	return func(c *Client) {
		c.logger = l
	}
}

// WithTracerProvider sets the OpenTelemetry TracerProvider for tracing
// workflow and activity executions. When set, spans are automatically
// created for workflow starts, activity executions, and signals via
// Temporal's OpenTelemetry interceptor.
func WithTracerProvider(tp trace.TracerProvider) ClientOption {
	return func(c *Client) {
		c.tracerProvider = tp
	}
}

// NewClient creates a new Client with the given options. The returned
// Client is not yet connected — call Start to establish the connection
// to the Temporal server.
//
// Default values:
//   - hostPort:  "localhost:7233"
//   - namespace: "default"
//   - logger:    platform.NopLogger()
func NewClient(opts ...ClientOption) (*Client, error) {
	c := &Client{
		hostPort:  "localhost:7233",
		namespace: "default",
		logger:    platform.NopLogger(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Start connects to the Temporal server using the configured hostPort
// and namespace. It implements platform.Component.
//
// Start uses client.Dial to establish the connection. If the dial fails,
// the error is wrapped and returned. Calling Start on an already-started
// Client returns an error.
func (c *Client) Start(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.temporalClient != nil {
		return fmt.Errorf("workflow: client already started")
	}

	dialOpts := client.Options{
		HostPort:  c.hostPort,
		Namespace: c.namespace,
	}

	// Attach OTel tracing interceptor when TracerProvider is configured.
	if c.tracerProvider != nil {
		tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
			Tracer: c.tracerProvider.Tracer("go.temporal.io/sdk"),
		})
		if err != nil {
			return fmt.Errorf("workflow: create tracing interceptor: %w", err)
		}
		dialOpts.Interceptors = append(dialOpts.Interceptors, tracingInterceptor)
	}

	tc, err := client.Dial(dialOpts)
	if err != nil {
		return fmt.Errorf("workflow: dial temporal server: %w", err)
	}

	c.temporalClient = tc
	c.logger.Info("workflow: client connected", "hostPort", c.hostPort, "namespace", c.namespace)
	return nil
}

// Stop closes the Temporal client connection. It implements
// platform.Component.
//
// Stop is safe to call multiple times; subsequent calls after the first
// successful close are no-ops. Stop is also safe to call before Start —
// it returns nil without error.
func (c *Client) Stop(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.temporalClient == nil {
		return nil
	}

	c.temporalClient.Close()
	c.temporalClient = nil
	c.logger.Info("workflow: client connection closed")
	return nil
}

// ExecuteWorkflow starts a workflow execution on the Temporal server and
// returns a WorkflowRun handle that can be used to retrieve the result.
//
// The wf parameter is the workflow function or a string workflow type
// name. Additional positional arguments are passed as workflow input.
//
// ExecuteWorkflow returns an error if the client has not been started or
// if the Temporal server rejects the request.
func (c *Client) ExecuteWorkflow(ctx context.Context, opts WorkflowOptions, wf any, args ...any) (WorkflowRun, error) {
	c.mu.Lock()
	tc := c.temporalClient
	c.mu.Unlock()

	if tc == nil {
		return nil, fmt.Errorf("workflow: client not started")
	}

	run, err := tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        opts.ID,
		TaskQueue: opts.TaskQueue,
	}, wf, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow: execute workflow: %w", err)
	}

	return &workflowRun{run: run}, nil
}

// SignalWorkflow sends a signal to a running workflow execution
// identified by workflowID. The signalName identifies which signal
// handler to invoke, and arg is the signal payload.
//
// SignalWorkflow returns an error if the client has not been started or
// if the Temporal server rejects the signal.
func (c *Client) SignalWorkflow(ctx context.Context, workflowID, signalName string, arg any) error {
	c.mu.Lock()
	tc := c.temporalClient
	c.mu.Unlock()

	if tc == nil {
		return fmt.Errorf("workflow: client not started")
	}

	if err := tc.SignalWorkflow(ctx, workflowID, "", signalName, arg); err != nil {
		return fmt.Errorf("workflow: signal workflow %q: %w", workflowID, err)
	}

	return nil
}

// TemporalClient returns the underlying Temporal SDK client for advanced
// usage such as querying workflow state, listing workflows, or using
// features not exposed by this wrapper.
//
// Returns nil if the Client has not been started.
func (c *Client) TemporalClient() client.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.temporalClient
}

// workflowRun adapts a Temporal SDK WorkflowRun to the WorkflowRun
// interface defined in this package.
type workflowRun struct {
	run client.WorkflowRun
}

// GetID returns the workflow ID of the execution.
func (r *workflowRun) GetID() string {
	return r.run.GetID()
}

// GetRunID returns the run ID of the execution.
func (r *workflowRun) GetRunID() string {
	return r.run.GetRunID()
}

// Get blocks until the workflow completes and decodes the result into
// the value pointed to by valuePtr.
func (r *workflowRun) Get(ctx context.Context, valuePtr any) error {
	return r.run.Get(ctx, valuePtr)
}
