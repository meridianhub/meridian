// Package validation provides dry-run validation for Starlark saga scripts.
package validation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"go.starlark.net/syntax"
)

// Validator errors.
var (
	ErrRuntimeRequired        = errors.New("runtime is required")
	ErrMockRegistryRequired   = errors.New("mock registry is required")
	ErrSchemaRegistryRequired = errors.New("schema registry is required")
)

// ErrorCategory classifies validation errors by their root cause.
type ErrorCategory string

const (
	// CategorySyntax indicates a Starlark syntax error (parse failure).
	CategorySyntax ErrorCategory = "SYNTAX"

	// CategoryUndefinedHandler indicates a handler is not found in the schema registry.
	CategoryUndefinedHandler ErrorCategory = "UNDEFINED_HANDLER"

	// CategoryTypeMismatch indicates wrong parameter types (caught by CoerceParams).
	CategoryTypeMismatch ErrorCategory = "TYPE_MISMATCH"

	// CategoryRuntime indicates a script runtime error (calls fail() or raises exception).
	CategoryRuntime ErrorCategory = "RUNTIME"

	// CategoryTimeout indicates a validation timeout (execution exceeded time limit).
	CategoryTimeout ErrorCategory = "TIMEOUT"
)

// ValidationError represents a single error found during validation.
//
//nolint:revive // validation.ValidationError is clearer than validation.Error
type ValidationError struct {
	// Line is the source line number where the error occurred (1-indexed, 0 if unknown).
	Line int `json:"line"`

	// Column is the source column number where the error occurred (1-indexed, 0 if unknown).
	Column int `json:"column"`

	// Message is the human-readable error message.
	Message string `json:"message"`

	// Category classifies the error type.
	Category ErrorCategory `json:"category"`
}

// ComplexityMetrics analyzes script complexity and provides execution estimates.
type ComplexityMetrics struct {
	// HandlerCallCount is the number of handler calls executed.
	HandlerCallCount int `json:"handler_call_count"`

	// OperationCount is the total number of Starlark operations (loops, conditionals, assignments).
	OperationCount int `json:"operation_count"`

	// EstimatedDuration is the projected execution time based on handler calls.
	// Formula: HandlerCallCount * 10ms (assumed RPC latency per handler).
	EstimatedDuration time.Duration `json:"estimated_duration"`

	// MaxDepth is the maximum nested handler call depth.
	MaxDepth int `json:"max_depth"`
}

// ValidationResult contains the complete result of a dry-run validation.
//
//nolint:revive // validation.ValidationResult is clearer than validation.Result
type ValidationResult struct {
	// Success is true if the script executed without errors.
	Success bool `json:"success"`

	// Errors contains all errors encountered during validation.
	Errors []ValidationError `json:"errors"`

	// Metrics provides complexity analysis even for failed validations.
	Metrics ComplexityMetrics `json:"metrics"`

	// StepResults contains the raw execution trace from the saga runner.
	StepResults []saga.StepResult `json:"step_results"`
}

// DryRunValidator validates Starlark saga scripts using mock handlers.
// It executes scripts in an isolated runtime with a 5-second timeout
// and captures errors, metrics, and execution traces.
type DryRunValidator struct {
	runtime        *saga.Runtime
	mockRegistry   *saga.HandlerRegistry
	schemaRegistry *schema.Registry
	logger         *slog.Logger
}

// DryRunValidatorConfig configures a DryRunValidator.
type DryRunValidatorConfig struct {
	// Runtime is the Starlark execution runtime (with 5s timeout).
	Runtime *saga.Runtime

	// MockRegistry contains mock handlers generated from the schema.
	MockRegistry *saga.HandlerRegistry

	// SchemaRegistry contains handler schema definitions for validation.
	SchemaRegistry *schema.Registry

	// Logger for structured logging.
	Logger *slog.Logger
}

// NewDryRunValidator creates a new dry-run validator.
func NewDryRunValidator(cfg DryRunValidatorConfig) (*DryRunValidator, error) {
	if cfg.Runtime == nil {
		return nil, ErrRuntimeRequired
	}
	if cfg.MockRegistry == nil {
		return nil, ErrMockRegistryRequired
	}
	if cfg.SchemaRegistry == nil {
		return nil, ErrSchemaRegistryRequired
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &DryRunValidator{
		runtime:        cfg.Runtime,
		mockRegistry:   cfg.MockRegistry,
		schemaRegistry: cfg.SchemaRegistry,
		logger:         logger,
	}, nil
}

// Validate executes a Starlark script in dry-run mode and returns validation results.
// The script is executed with mock handlers and a 5-second timeout.
// All errors are captured and classified, and complexity metrics are calculated.
func (v *DryRunValidator) Validate(ctx context.Context, script string) (*ValidationResult, error) {
	result := &ValidationResult{
		Success: false,
		Errors:  []ValidationError{},
		Metrics: ComplexityMetrics{},
	}

	// Parse the script to check for syntax errors and calculate operation count
	fileOpts := &syntax.FileOptions{
		Set:            true,
		While:          false, // Starlark doesn't support while loops
		GlobalReassign: true,
		Recursion:      false, // Prevent infinite recursion
	}
	fileNode, err := fileOpts.Parse("saga.star", script, 0)
	if err != nil {
		// Syntax error - extract line/column info
		result.Errors = append(result.Errors, classifySyntaxError(err))
		return result, nil // Return result with errors, not error
	}

	// Calculate operation count from AST
	result.Metrics.OperationCount = countOperations(fileNode)

	// Build service modules from mock registry using the schema registry definitions.
	// Mock handlers don't have proto metadata, so we use BuildServiceModulesFromSchema
	// with the YAML-based schema registry to preserve parameter type validation.
	serviceModules, err := schema.BuildServiceModulesFromSchema(v.mockRegistry, v.schemaRegistry.ToSchema())
	if err != nil {
		return nil, fmt.Errorf("failed to build service modules: %w", err)
	}

	// Create Starlark saga runner with mock handlers
	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        v.runtime,
		Registry:       v.mockRegistry,
		ServiceModules: serviceModules,
		Logger:         v.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create saga runner: %w", err)
	}

	// Execute the script with mock input
	runnerInput := saga.RunnerInput{
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		Input:           map[string]interface{}{},
	}

	runnerOutput, err := runner.ExecuteSaga(ctx, "dry-run", script, runnerInput)
	if err != nil {
		// Setup/internal error (not a script error)
		return nil, fmt.Errorf("saga runner error: %w", err)
	}

	// Always capture step results and calculate metrics
	result.StepResults = runnerOutput.StepResults
	metrics := calculateMetrics(runnerOutput.StepResults)
	// Preserve OperationCount from AST analysis
	metrics.OperationCount = result.Metrics.OperationCount
	result.Metrics = metrics

	// Check if execution failed (via Success flag)
	if !runnerOutput.Success {
		// Execution failed - create error from RunnerOutput.Error field
		result.Errors = append(result.Errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  runnerOutput.Error,
			Category: classifyErrorMessage(runnerOutput.Error),
		})
		result.Success = false
		return result, nil
	}

	// Execution succeeded
	result.Success = true

	return result, nil
}

// classifySyntaxError converts a Starlark syntax error to a ValidationError.
func classifySyntaxError(err error) ValidationError {
	// Try to extract position from syntax.Error
	var syntaxErr syntax.Error
	if errors.As(err, &syntaxErr) {
		return ValidationError{
			Line:     int(syntaxErr.Pos.Line),
			Column:   int(syntaxErr.Pos.Col),
			Message:  syntaxErr.Msg,
			Category: CategorySyntax,
		}
	}

	// Fallback for other error types
	return ValidationError{
		Line:     0,
		Column:   0,
		Message:  err.Error(),
		Category: CategorySyntax,
	}
}

// classifyErrorMessage classifies an error message string into a category.
func classifyErrorMessage(errMsg string) ErrorCategory {
	// Check for undefined handler errors
	if strings.Contains(errMsg, "not found in registry") ||
		strings.Contains(errMsg, "undefined:") {
		return CategoryUndefinedHandler
	}

	// Check for type mismatch errors (from CoerceParams)
	if strings.Contains(errMsg, "type mismatch") ||
		strings.Contains(errMsg, "cannot convert") ||
		strings.Contains(errMsg, "expected") {
		return CategoryTypeMismatch
	}

	// Default to runtime error (includes fail(), exceptions, etc.)
	return CategoryRuntime
}

// calculateMetrics derives complexity metrics from step results.
// The OperationCount is NOT calculated here - it's calculated during AST parsing
// before execution and stored separately in ValidationResult.
func calculateMetrics(stepResults []saga.StepResult) ComplexityMetrics {
	handlerCount := len(stepResults)

	// Calculate max depth by tracking nested calls
	// (In this simple implementation, depth is 1 since we don't track nesting yet)
	maxDepth := 0
	if handlerCount > 0 {
		maxDepth = 1
	}

	// Estimated duration: 10ms per handler call
	estimatedDuration := time.Duration(handlerCount) * 10 * time.Millisecond

	return ComplexityMetrics{
		HandlerCallCount:  handlerCount,
		OperationCount:    0, // Set during AST parsing, preserved when merging metrics
		EstimatedDuration: estimatedDuration,
		MaxDepth:          maxDepth,
	}
}

// countOperations traverses the AST and counts operations (loops, conditionals, assignments).
func countOperations(fileNode *syntax.File) int {
	count := 0

	// Walk the AST and count operation nodes
	var walk func(node syntax.Node)
	walk = func(node syntax.Node) {
		if node == nil {
			return
		}

		switch n := node.(type) {
		case *syntax.ForStmt:
			count++
			walkStmts(n.Body, walk)
		case *syntax.IfStmt:
			count++
			walkStmts(n.True, walk)
			walkStmts(n.False, walk)
		case *syntax.AssignStmt:
			count++
		case *syntax.ExprStmt:
			// Count function calls
			if _, ok := n.X.(*syntax.CallExpr); ok {
				count++
			}
		case *syntax.File:
			walkStmts(n.Stmts, walk)
		case *syntax.DefStmt:
			walkStmts(n.Body, walk)
		}
	}

	walk(fileNode)
	return count
}

// walkStmts is a helper to walk a list of statements.
func walkStmts(stmts []syntax.Stmt, walk func(syntax.Node)) {
	for _, stmt := range stmts {
		walk(stmt)
	}
}

// NewMockValidatorForTesting creates a pre-configured validator for testing.
// It registers mock handlers from the schema and returns a ready-to-use validator.
func NewMockValidatorForTesting(schemaRegistry *schema.Registry) (*DryRunValidator, error) {
	// Create runtime with 5s timeout
	runtime, err := saga.NewRuntime(slog.Default(), saga.WithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime: %w", err)
	}

	// Create mock registry and register mock handlers
	mockRegistry := saga.NewHandlerRegistry()
	if err := RegisterMockHandlers(mockRegistry, schemaRegistry); err != nil {
		return nil, fmt.Errorf("failed to register mock handlers: %w", err)
	}

	// Create validator
	return NewDryRunValidator(DryRunValidatorConfig{
		Runtime:        runtime,
		MockRegistry:   mockRegistry,
		SchemaRegistry: schemaRegistry,
		Logger:         slog.Default(),
	})
}
