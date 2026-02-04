package saga

import (
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
)

// CEL evaluation errors.
var (
	// ErrCELCompilationFailed is returned when CEL expression compilation fails.
	ErrCELCompilationFailed = errors.New("CEL compilation failed")

	// ErrCELEvaluationFailed is returned when CEL expression evaluation fails.
	ErrCELEvaluationFailed = errors.New("CEL evaluation failed")
)

// CELEvaluator provides CEL expression evaluation for saga scripts.
// It maintains a CEL environment with saga-specific variables and provides
// methods to compile and evaluate CEL expressions within the saga context.
type CELEvaluator struct {
	env *cel.Env
}

// NewCELEvaluator creates a CEL evaluator with saga-specific environment.
// The environment includes the following variables:
//   - input: map[string]any - saga input parameters
//   - ctx: map[string]string - saga context metadata (saga_execution_id, correlation_id)
func NewCELEvaluator() (*CELEvaluator, error) {
	env, err := cel.NewEnv(
		cel.Variable("input", cel.DynType),
		cel.Variable("ctx", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}
	return &CELEvaluator{env: env}, nil
}

// Eval compiles and evaluates a CEL expression with the given variables.
// Returns the evaluation result as a native Go value, or an error if compilation
// or evaluation fails.
func (e *CELEvaluator) Eval(expression string, variables map[string]interface{}) (interface{}, error) {
	// Compile expression
	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrCELCompilationFailed, issues.Err())
	}

	// Create program
	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program: %w", err)
	}

	// Evaluate
	result, _, err := prg.Eval(variables)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCELEvaluationFailed, err)
	}

	// Convert CEL result to Go value
	return result.Value(), nil
}
