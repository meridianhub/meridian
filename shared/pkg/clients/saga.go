package clients

import (
	"context"
	"fmt"
	"log/slog"
)

// SagaStep represents a single step in a saga transaction
type SagaStep struct {
	Name       string
	Action     func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

// SagaOrchestrator manages distributed transactions with compensation
type SagaOrchestrator struct {
	steps  []SagaStep
	logger *slog.Logger
}

// SagaResult contains the outcome of a saga execution
type SagaResult struct {
	Success          bool
	CompletedSteps   int
	FailedStep       string
	CompensatedSteps int
	Error            error
	// CompensationErrors contains errors from failed compensation attempts.
	// Even if some compensations fail, the saga continues compensating remaining steps.
	CompensationErrors []error
}

// NewSagaOrchestrator creates a new saga orchestrator
func NewSagaOrchestrator(logger *slog.Logger) *SagaOrchestrator {
	if logger == nil {
		logger = slog.Default()
	}

	return &SagaOrchestrator{
		steps:  make([]SagaStep, 0),
		logger: logger,
	}
}

// AddStep adds a step to the saga
func (s *SagaOrchestrator) AddStep(name string, action, compensate func(ctx context.Context) error) {
	s.steps = append(s.steps, SagaStep{
		Name:       name,
		Action:     action,
		Compensate: compensate,
	})
}

// Execute runs the saga, performing compensation on failure
func (s *SagaOrchestrator) Execute(ctx context.Context) SagaResult {
	result := SagaResult{
		Success: true,
	}

	// Execute steps in order
	for i, step := range s.steps {
		s.logger.Info("executing saga step",
			"step", step.Name,
			"index", i+1,
			"total", len(s.steps))

		if err := step.Action(ctx); err != nil {
			s.logger.Error("saga step failed",
				"step", step.Name,
				"error", err)

			result.Success = false
			result.CompletedSteps = i
			result.FailedStep = step.Name
			result.Error = fmt.Errorf("step %s failed: %w", step.Name, err)

			// Compensate completed steps in reverse order
			s.compensate(ctx, i, &result)

			return result
		}

		result.CompletedSteps++
	}

	s.logger.Info("saga completed successfully",
		"total_steps", len(s.steps))

	return result
}

// compensate performs compensation for completed steps in reverse order
func (s *SagaOrchestrator) compensate(ctx context.Context, failedAtIndex int, result *SagaResult) {
	s.logger.Info("starting compensation",
		"completed_steps", failedAtIndex)

	// Compensate in reverse order (LIFO)
	for i := failedAtIndex - 1; i >= 0; i-- {
		step := s.steps[i]

		if step.Compensate == nil {
			s.logger.Warn("no compensation defined for step",
				"step", step.Name)
			continue
		}

		s.logger.Info("compensating step",
			"step", step.Name,
			"index", i+1)

		if err := step.Compensate(ctx); err != nil {
			s.logger.Error("compensation failed",
				"step", step.Name,
				"error", err)
			// Track compensation error and continue with remaining steps
			result.CompensationErrors = append(result.CompensationErrors,
				fmt.Errorf("compensation for step %s failed: %w", step.Name, err))
		} else {
			result.CompensatedSteps++
		}
	}

	s.logger.Info("compensation completed",
		"compensated_steps", result.CompensatedSteps)
}

// Reset clears all steps from the orchestrator
func (s *SagaOrchestrator) Reset() {
	s.steps = make([]SagaStep, 0)
}

// StepCount returns the number of steps in the saga
func (s *SagaOrchestrator) StepCount() int {
	return len(s.steps)
}
