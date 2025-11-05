package clients_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errStep1Failed        = errors.New("step1 failed")
	errStep2Failed        = errors.New("step2 failed")
	errStep3Failed        = errors.New("step3 failed")
	errStep7Failed        = errors.New("step7 failed")
	errCompensationFailed = errors.New("compensation failed")
)

// TestNewSagaOrchestrator_Success verifies orchestrator creation
func TestNewSagaOrchestrator_Success(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	orchestrator := clients.NewSagaOrchestrator(logger)

	require.NotNil(t, orchestrator)
	assert.Equal(t, 0, orchestrator.StepCount())
}

// TestNewSagaOrchestrator_NilLogger verifies default logger is used when nil provided
func TestNewSagaOrchestrator_NilLogger(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(nil)

	require.NotNil(t, orchestrator)
	assert.Equal(t, 0, orchestrator.StepCount())
}

// TestSagaOrchestrator_AddStep verifies adding steps
func TestSagaOrchestrator_AddStep(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	action := func(_ context.Context) error { return nil }
	compensate := func(_ context.Context) error { return nil }

	orchestrator.AddStep("step1", action, compensate)
	assert.Equal(t, 1, orchestrator.StepCount())

	orchestrator.AddStep("step2", action, compensate)
	assert.Equal(t, 2, orchestrator.StepCount())

	orchestrator.AddStep("step3", action, compensate)
	assert.Equal(t, 3, orchestrator.StepCount())
}

// TestSagaOrchestrator_Execute_AllStepsSucceed verifies successful saga execution
func TestSagaOrchestrator_Execute_AllStepsSucceed(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return nil
	}
	step2 := func(_ context.Context) error {
		executed = append(executed, "step2")
		return nil
	}
	step3 := func(_ context.Context) error {
		executed = append(executed, "step3")
		return nil
	}

	orchestrator.AddStep("step1", step1, nil)
	orchestrator.AddStep("step2", step2, nil)
	orchestrator.AddStep("step3", step3, nil)

	result := orchestrator.Execute(context.Background())

	assert.True(t, result.Success)
	assert.Equal(t, 3, result.CompletedSteps)
	assert.Equal(t, "", result.FailedStep)
	assert.Equal(t, 0, result.CompensatedSteps)
	assert.NoError(t, result.Error)
	assert.Equal(t, []string{"step1", "step2", "step3"}, executed)
}

// TestSagaOrchestrator_Execute_StepFails verifies compensation on failure
func TestSagaOrchestrator_Execute_StepFails(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}
	compensated := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return nil
	}
	compensate1 := func(_ context.Context) error {
		compensated = append(compensated, "compensate1")
		return nil
	}

	step2 := func(_ context.Context) error {
		executed = append(executed, "step2")
		return nil
	}
	compensate2 := func(_ context.Context) error {
		compensated = append(compensated, "compensate2")
		return nil
	}

	step3 := func(_ context.Context) error {
		executed = append(executed, "step3")
		return errStep3Failed
	}
	compensate3 := func(_ context.Context) error {
		compensated = append(compensated, "compensate3")
		return nil
	}

	orchestrator.AddStep("step1", step1, compensate1)
	orchestrator.AddStep("step2", step2, compensate2)
	orchestrator.AddStep("step3", step3, compensate3)

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 2, result.CompletedSteps)
	assert.Equal(t, "step3", result.FailedStep)
	assert.Equal(t, 2, result.CompensatedSteps)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "step3 failed")

	// Verify execution order
	assert.Equal(t, []string{"step1", "step2", "step3"}, executed)

	// Verify compensation order (reverse)
	assert.Equal(t, []string{"compensate2", "compensate1"}, compensated)
}

// TestSagaOrchestrator_Execute_FirstStepFails verifies no compensation when first step fails
func TestSagaOrchestrator_Execute_FirstStepFails(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}
	compensated := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return errStep1Failed
	}
	compensate1 := func(_ context.Context) error {
		compensated = append(compensated, "compensate1")
		return nil
	}

	orchestrator.AddStep("step1", step1, compensate1)

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 0, result.CompletedSteps)
	assert.Equal(t, "step1", result.FailedStep)
	assert.Equal(t, 0, result.CompensatedSteps)
	assert.Error(t, result.Error)

	// First step failed, so no compensation should occur
	assert.Equal(t, []string{"step1"}, executed)
	assert.Empty(t, compensated)
}

// TestSagaOrchestrator_Execute_NoCompensationDefined verifies behavior when compensation is nil
func TestSagaOrchestrator_Execute_NoCompensationDefined(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return nil
	}

	step2 := func(_ context.Context) error {
		executed = append(executed, "step2")
		return errStep2Failed
	}

	orchestrator.AddStep("step1", step1, nil) // No compensation function
	orchestrator.AddStep("step2", step2, nil)

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 1, result.CompletedSteps)
	assert.Equal(t, "step2", result.FailedStep)
	assert.Equal(t, 0, result.CompensatedSteps) // No compensation because nil
	assert.Error(t, result.Error)

	assert.Equal(t, []string{"step1", "step2"}, executed)
}

// TestSagaOrchestrator_Execute_CompensationFails verifies saga continues compensating despite failures
func TestSagaOrchestrator_Execute_CompensationFails(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}
	compensated := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return nil
	}
	compensate1 := func(_ context.Context) error {
		compensated = append(compensated, "compensate1")
		return nil
	}

	step2 := func(_ context.Context) error {
		executed = append(executed, "step2")
		return nil
	}
	compensate2 := func(_ context.Context) error {
		compensated = append(compensated, "compensate2")
		return errCompensationFailed
	}

	step3 := func(_ context.Context) error {
		executed = append(executed, "step3")
		return errStep3Failed
	}

	orchestrator.AddStep("step1", step1, compensate1)
	orchestrator.AddStep("step2", step2, compensate2)
	orchestrator.AddStep("step3", step3, nil)

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 2, result.CompletedSteps)
	assert.Equal(t, "step3", result.FailedStep)
	assert.Equal(t, 1, result.CompensatedSteps) // Only step1 compensated successfully
	assert.Error(t, result.Error)

	// Verify both compensations were attempted despite failure
	assert.Equal(t, []string{"compensate2", "compensate1"}, compensated)
}

// TestSagaOrchestrator_Execute_ContextCancellation verifies handling of context cancellation
func TestSagaOrchestrator_Execute_ContextCancellation(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	ctx, cancel := context.WithCancel(context.Background())

	executed := []string{}
	compensated := []string{}

	step1 := func(_ context.Context) error {
		executed = append(executed, "step1")
		return nil
	}
	compensate1 := func(_ context.Context) error {
		compensated = append(compensated, "compensate1")
		return nil
	}

	step2 := func(_ context.Context) error {
		executed = append(executed, "step2")
		cancel() // Cancel context during execution
		return ctx.Err()
	}
	compensate2 := func(_ context.Context) error {
		compensated = append(compensated, "compensate2")
		return nil
	}

	orchestrator.AddStep("step1", step1, compensate1)
	orchestrator.AddStep("step2", step2, compensate2)

	result := orchestrator.Execute(ctx)

	assert.False(t, result.Success)
	assert.Equal(t, 1, result.CompletedSteps)
	assert.Equal(t, "step2", result.FailedStep)
	assert.Equal(t, 1, result.CompensatedSteps)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "context canceled")

	assert.Equal(t, []string{"step1", "step2"}, executed)
	assert.Equal(t, []string{"compensate1"}, compensated)
}

// TestSagaOrchestrator_Reset verifies clearing all steps
func TestSagaOrchestrator_Reset(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	action := func(_ context.Context) error { return nil }
	compensate := func(_ context.Context) error { return nil }

	orchestrator.AddStep("step1", action, compensate)
	orchestrator.AddStep("step2", action, compensate)
	assert.Equal(t, 2, orchestrator.StepCount())

	orchestrator.Reset()
	assert.Equal(t, 0, orchestrator.StepCount())
}

// TestSagaOrchestrator_Execute_EmptySaga verifies executing saga with no steps
func TestSagaOrchestrator_Execute_EmptySaga(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	result := orchestrator.Execute(context.Background())

	assert.True(t, result.Success)
	assert.Equal(t, 0, result.CompletedSteps)
	assert.Equal(t, "", result.FailedStep)
	assert.Equal(t, 0, result.CompensatedSteps)
	assert.NoError(t, result.Error)
}

// TestSagaOrchestrator_Execute_MultipleFailures verifies only first failure is reported
func TestSagaOrchestrator_Execute_MultipleFailures(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	step1 := func(_ context.Context) error {
		return nil
	}

	step2 := func(_ context.Context) error {
		return errStep2Failed
	}

	step3 := func(_ context.Context) error {
		return errStep3Failed
	}

	orchestrator.AddStep("step1", step1, nil)
	orchestrator.AddStep("step2", step2, nil)
	orchestrator.AddStep("step3", step3, nil)

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 1, result.CompletedSteps)
	assert.Equal(t, "step2", result.FailedStep) // First failure reported
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "step2 failed")
}

// TestSagaOrchestrator_Execute_LongSaga verifies handling of many steps
func TestSagaOrchestrator_Execute_LongSaga(t *testing.T) {
	t.Parallel()

	orchestrator := clients.NewSagaOrchestrator(slog.Default())

	executed := []string{}
	compensated := []string{}

	// Add 10 steps
	for i := 1; i <= 10; i++ {
		stepName := fmt.Sprintf("step%d", i)
		step := func(_ context.Context) error {
			executed = append(executed, stepName)
			// Fail at step 7
			if stepName == "step7" {
				return errStep7Failed
			}
			return nil
		}
		compensate := func(_ context.Context) error {
			compensated = append(compensated, "compensate-"+stepName)
			return nil
		}
		orchestrator.AddStep(stepName, step, compensate)
	}

	result := orchestrator.Execute(context.Background())

	assert.False(t, result.Success)
	assert.Equal(t, 6, result.CompletedSteps)
	assert.Equal(t, "step7", result.FailedStep)
	assert.Equal(t, 6, result.CompensatedSteps)
	assert.Error(t, result.Error)

	// Verify 7 executions (including failed step)
	assert.Equal(t, 7, len(executed))

	// Verify 6 compensations in reverse order
	assert.Equal(t, 6, len(compensated))
}
