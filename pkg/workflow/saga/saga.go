// Package saga provides a step builder for Temporal-based saga workflows.
//
// A saga is a sequence of steps where each step has an action and a
// compensation. If any step fails, previously completed steps are
// compensated in reverse order.
//
// Usage in a Temporal workflow:
//
//	func OrderSaga(ctx workflow.Context, input OrderInput) error {
//	    return saga.New(ctx).
//	        Step(ReserveStock, ReleaseStock).
//	        Step(ChargePayment, RefundPayment).
//	        Step(CreateShipment, CancelShipment).
//	        Execute(input)
//	}
package saga

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Action is a Temporal activity function executed as the forward step.
type Action any

// Compensation is a Temporal activity function executed on rollback.
type Compensation any

type step struct {
	action       Action
	compensation Compensation
}

// Saga builds and executes a sequence of compensatable steps.
type Saga struct {
	ctx   workflow.Context
	steps []step
	opts  options
}

type options struct {
	parallelCompensation bool
	continueOnError      bool
	activityOptions      *workflow.ActivityOptions
}

// Option configures saga execution behavior.
type Option func(*options)

// WithParallelCompensation runs all compensations concurrently instead of
// sequentially in reverse order. Use when compensations are independent.
func WithParallelCompensation() Option {
	return func(o *options) {
		o.parallelCompensation = true
	}
}

// WithActivityOptions sets custom ActivityOptions for all saga steps.
// If not set, a default with StartToCloseTimeout=30s and no retry is used.
func WithActivityOptions(ao workflow.ActivityOptions) Option {
	return func(o *options) {
		o.activityOptions = &ao
	}
}

// WithContinueOnCompensationError continues executing remaining compensations
// even if one fails. By default, compensation stops on first error.
func WithContinueOnCompensationError() Option {
	return func(o *options) {
		o.continueOnError = true
	}
}

// New creates a new Saga builder with the given Temporal workflow context.
func New(ctx workflow.Context, opts ...Option) *Saga {
	s := &Saga{ctx: ctx}
	for _, opt := range opts {
		opt(&s.opts)
	}
	return s
}

// Step adds a forward action and its compensation to the saga.
// Actions are executed in order. If an action fails, compensations for
// all previously completed steps are executed in reverse order.
//
// Both action and compensation must be valid Temporal activity functions.
// The compensation receives the same input as the action.
func (s *Saga) Step(action Action, compensation Compensation) *Saga {
	s.steps = append(s.steps, step{
		action:       action,
		compensation: compensation,
	})
	return s
}

// Execute runs all saga steps in order. If any step fails, compensations
// for completed steps are executed in reverse order (or in parallel if
// WithParallelCompensation was set).
//
// The input is passed to every action and compensation activity.
// Returns the original action error wrapped with compensation errors if any.
func (s *Saga) Execute(input any) error {
	// Apply activity options to context.
	ctx := s.ctx
	ao := s.opts.activityOptions
	if ao == nil {
		ao = &workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}
	}
	ctx = workflow.WithActivityOptions(ctx, *ao)

	var completed []step

	for i, st := range s.steps {
		err := workflow.ExecuteActivity(ctx, st.action, input).Get(ctx, nil)
		if err != nil {
			compErr := s.compensate(ctx, completed, input)
			if compErr != nil {
				return fmt.Errorf("saga: step %d failed: %w; compensation error: %v", i, err, compErr)
			}
			return fmt.Errorf("saga: step %d failed: %w", i, err)
		}
		completed = append(completed, st)
	}

	return nil
}

// compensate executes compensations for completed steps.
func (s *Saga) compensate(ctx workflow.Context, completed []step, input any) error {
	if len(completed) == 0 {
		return nil
	}

	if s.opts.parallelCompensation {
		return s.compensateParallel(ctx, completed, input)
	}
	return s.compensateSequential(ctx, completed, input)
}

// compensateSequential runs compensations in reverse order.
func (s *Saga) compensateSequential(ctx workflow.Context, completed []step, input any) error {
	var errs []error
	for i := len(completed) - 1; i >= 0; i-- {
		st := completed[i]
		if st.compensation == nil {
			continue
		}
		err := workflow.ExecuteActivity(ctx, st.compensation, input).Get(ctx, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("compensation %d: %w", i, err))
			if !s.opts.continueOnError {
				break
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}
	return nil
}

// compensateParallel runs all compensations concurrently.
func (s *Saga) compensateParallel(ctx workflow.Context, completed []step, input any) error {
	var futures []workflow.Future
	var indices []int

	for i := len(completed) - 1; i >= 0; i-- {
		st := completed[i]
		if st.compensation == nil {
			continue
		}
		f := workflow.ExecuteActivity(ctx, st.compensation, input)
		futures = append(futures, f)
		indices = append(indices, i)
	}

	var errs []error
	for j, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			errs = append(errs, fmt.Errorf("compensation %d: %w", indices[j], err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%v", errs)
	}
	return nil
}
