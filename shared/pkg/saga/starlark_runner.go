// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Runner errors.
var (
	// ErrRuntimeRequired is returned when Runtime is nil.
	ErrRuntimeRequired = errors.New("runtime is required")

	// ErrRegistryRequired is returned when Registry is nil.
	ErrRegistryRequired = errors.New("registry is required")
)

// StarlarkSagaRunner executes saga definitions written in Starlark.
// It integrates the Starlark Runtime with the DomainHandlerRegistry to enable
// Starlark scripts to call domain-specific handlers.
type StarlarkSagaRunner struct {
	runtime  *Runtime
	registry *DomainHandlerRegistry
	logger   *slog.Logger
}

// StarlarkSagaRunnerConfig contains configuration for creating a StarlarkSagaRunner.
type StarlarkSagaRunnerConfig struct {
	// Runtime is the Starlark execution runtime.
	Runtime *Runtime

	// Registry contains domain-specific handlers that Starlark scripts can call.
	Registry *DomainHandlerRegistry

	// Logger for structured logging.
	Logger *slog.Logger
}

// NewStarlarkSagaRunner creates a new StarlarkSagaRunner.
func NewStarlarkSagaRunner(cfg StarlarkSagaRunnerConfig) (*StarlarkSagaRunner, error) {
	if cfg.Runtime == nil {
		return nil, ErrRuntimeRequired
	}
	if cfg.Registry == nil {
		return nil, ErrRegistryRequired
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &StarlarkSagaRunner{
		runtime:  cfg.Runtime,
		registry: cfg.Registry,
		logger:   logger,
	}, nil
}

// RunnerInput contains the input parameters for a saga execution.
type RunnerInput struct {
	// SagaExecutionID is the unique identifier for this saga execution.
	SagaExecutionID uuid.UUID

	// CorrelationID groups all related actions across the entire business operation.
	CorrelationID uuid.UUID

	// PartyScope restricts data access to a specific party (tenant isolation).
	PartyScope *PartyScope

	// KnowledgeAt enables bi-temporal queries - what we knew at a specific point in time.
	KnowledgeAt time.Time

	// Input contains the saga-specific input parameters.
	Input map[string]interface{}
}

// RunnerOutput contains the result of a saga execution.
type RunnerOutput struct {
	// Success indicates whether the saga completed successfully.
	Success bool

	// Output contains the saga's output variables.
	Output map[string]interface{}

	// Error contains the error message if Success is false.
	Error string

	// StepResults contains the results of each step for debugging.
	StepResults []StepResult
}

// StepResult captures the result of a single saga step.
type StepResult struct {
	// StepName is the handler name that was called.
	StepName string

	// Success indicates whether the step succeeded.
	Success bool

	// Output contains the step's output.
	Output interface{}

	// Error contains the error message if Success is false.
	Error string

	// Duration is how long the step took to execute.
	Duration time.Duration
}

// ExecuteSaga executes a Starlark saga script with the given input.
// The script can call domain handlers via the domain_call builtin function.
func (r *StarlarkSagaRunner) ExecuteSaga(ctx context.Context, sagaName string, script string, input RunnerInput) (*RunnerOutput, error) {
	logger := r.logger.With(
		"saga_name", sagaName,
		"saga_execution_id", input.SagaExecutionID.String(),
		"correlation_id", input.CorrelationID.String(),
	)

	logger.Info("starting Starlark saga execution")

	// Create StarlarkContext for domain handlers
	starlarkCtx := &StarlarkContext{
		Context:         ctx,
		PartyScope:      input.PartyScope,
		SagaExecutionID: input.SagaExecutionID,
		CorrelationID:   input.CorrelationID,
		KnowledgeAt:     input.KnowledgeAt,
		Logger:          logger,
		LookupCache:     NewLookupResultCache(),
	}

	// Track step results
	var stepResults []StepResult

	// Create domain_call function that invokes registered handlers
	domainCall := func(handlerName string, params map[string]interface{}) (interface{}, error) {
		stepStart := time.Now()

		logger.Debug("domain_call invoked", "handler", handlerName)

		handler, err := r.registry.Get(handlerName)
		if err != nil {
			stepResults = append(stepResults, StepResult{
				StepName: handlerName,
				Success:  false,
				Error:    err.Error(),
				Duration: time.Since(stepStart),
			})
			return nil, fmt.Errorf("handler %s: %w", handlerName, err)
		}

		// Convert params to map[string]any for handler
		paramsAny := make(map[string]any, len(params))
		for k, v := range params {
			paramsAny[k] = v
		}

		result, err := handler(starlarkCtx, paramsAny)
		duration := time.Since(stepStart)

		if err != nil {
			stepResults = append(stepResults, StepResult{
				StepName: handlerName,
				Success:  false,
				Error:    err.Error(),
				Duration: duration,
			})
			logger.Warn("domain handler failed",
				"handler", handlerName,
				"error", err,
				"duration_ms", duration.Milliseconds())
			return nil, err
		}

		stepResults = append(stepResults, StepResult{
			StepName: handlerName,
			Success:  true,
			Output:   result,
			Duration: duration,
		})

		logger.Debug("domain handler succeeded",
			"handler", handlerName,
			"duration_ms", duration.Milliseconds())

		return result, nil
	}

	// Build input for Starlark script
	scriptInput := make(map[string]interface{})
	for k, v := range input.Input {
		scriptInput[k] = v
	}

	// Add domain_call as a special input that can be invoked
	scriptInput["_domain_call"] = domainCall
	scriptInput["_saga_execution_id"] = input.SagaExecutionID.String()
	scriptInput["_correlation_id"] = input.CorrelationID.String()

	// Execute the Starlark script
	result, err := r.runtime.ExecuteSaga(ctx, sagaName, script, scriptInput)
	if err != nil {
		logger.Error("Starlark saga execution failed", "error", err)
		return &RunnerOutput{
			Success:     false,
			Error:       err.Error(),
			StepResults: stepResults,
		}, nil
	}

	// Check if the saga was suspended
	if starlarkCtx.IsSuspended() {
		logger.Info("saga suspended",
			"reason", starlarkCtx.SuspendReason,
			"resume_after", starlarkCtx.ResumeAfter)
		return &RunnerOutput{
			Success:     false,
			Output:      result.Globals,
			Error:       fmt.Sprintf("saga suspended: %s", starlarkCtx.SuspendReason),
			StepResults: stepResults,
		}, nil
	}

	logger.Info("Starlark saga execution completed",
		"step_count", len(stepResults),
		"success", true)

	return &RunnerOutput{
		Success:     true,
		Output:      result.Globals,
		StepResults: stepResults,
	}, nil
}

// WithLogger returns a new runner with the given logger.
func (r *StarlarkSagaRunner) WithLogger(logger *slog.Logger) *StarlarkSagaRunner {
	if logger == nil {
		logger = slog.Default()
	}
	return &StarlarkSagaRunner{
		runtime:  r.runtime,
		registry: r.registry,
		logger:   logger,
	}
}
