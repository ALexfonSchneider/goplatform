package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// Test activities — must accept context.Context as first arg per Temporal convention.
func actStep1(_ context.Context, _ string) error  { return nil }
func actStep2(_ context.Context, _ string) error  { return nil }
func actStep3(_ context.Context, _ string) error  { return nil }
func compStep1(_ context.Context, _ string) error { return nil }
func compStep2(_ context.Context, _ string) error { return nil }
func compStep3(_ context.Context, _ string) error { return nil }

func TestSaga_AllStepsSucceed(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(actStep3)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep3, mock.Anything, "input").Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Step(actStep3, compStep3).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestSaga_Step2Fails_CompensatesStep1(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(compStep1)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(errors.New("step2 failed"))
	env.OnActivity(compStep1, mock.Anything, "input").Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Step(actStep3, compStep3).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 1 failed")

	// compStep1 was called, actStep3 was never called.
	env.AssertExpectations(t)
}

func TestSaga_Step3Fails_CompensatesStep2AndStep1(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(actStep3)
	env.RegisterActivity(compStep1)
	env.RegisterActivity(compStep2)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep3, mock.Anything, "input").Return(errors.New("step3 failed"))
	env.OnActivity(compStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(compStep1, mock.Anything, "input").Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Step(actStep3, compStep3).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "step 2 failed")
	env.AssertExpectations(t)
}

func TestSaga_NilCompensation_Skipped(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(errors.New("fail"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx).
			Step(actStep1, nil). // no compensation
			Step(actStep2, compStep2).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// No panic — nil compensation is skipped.
	env.AssertExpectations(t)
}

func TestSaga_CompensationFails_ReturnsWrappedError(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(compStep1)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(errors.New("action failed"))
	env.OnActivity(compStep1, mock.Anything, "input").Return(errors.New("comp failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compensation error")
}

func TestSaga_ContinueOnCompensationError(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(actStep3)
	env.RegisterActivity(compStep1)
	env.RegisterActivity(compStep2)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep3, mock.Anything, "input").Return(errors.New("fail"))
	env.OnActivity(compStep2, mock.Anything, "input").Return(errors.New("comp2 failed"))
	env.OnActivity(compStep1, mock.Anything, "input").Return(nil) // still called

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx, WithContinueOnCompensationError()).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Step(actStep3, compStep3).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	// Both compStep2 and compStep1 were called despite comp2 failure.
	env.AssertExpectations(t)
}

func TestSaga_ParallelCompensation(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.RegisterActivity(actStep1)
	env.RegisterActivity(actStep2)
	env.RegisterActivity(actStep3)
	env.RegisterActivity(compStep1)
	env.RegisterActivity(compStep2)

	env.OnActivity(actStep1, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(actStep3, mock.Anything, "input").Return(errors.New("fail"))
	env.OnActivity(compStep2, mock.Anything, "input").Return(nil)
	env.OnActivity(compStep1, mock.Anything, "input").Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return New(ctx, WithParallelCompensation()).
			Step(actStep1, compStep1).
			Step(actStep2, compStep2).
			Step(actStep3, compStep3).
			Execute("input")
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
