package saga

import (
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/interpreter"
)

// DefaultCELCostLimit bounds the runtime cost of a single saga CEL expression.
// CEL cost is an abstract metric of the number and expense of operations performed
// during evaluation (indicative of CPU usage). Enforcing a limit upholds Meridian's
// bounded-execution guarantee: a tenant expression cannot run unboundedly expensive
// computation. The value mirrors the per-call budget Kubernetes uses for its CEL
// admission rules, which keeps well-formed pricing/validation expressions sub-millisecond.
const DefaultCELCostLimit uint64 = 1_000_000

// CEL evaluation errors.
var (
	// ErrCELCompilationFailed is returned when CEL expression compilation fails.
	ErrCELCompilationFailed = errors.New("CEL compilation failed")

	// ErrCELEvaluationFailed is returned when CEL expression evaluation fails.
	ErrCELEvaluationFailed = errors.New("CEL evaluation failed")

	// ErrCELCostLimitExceeded is returned when evaluation is cancelled because the
	// expression exceeded the configured runtime cost limit.
	ErrCELCostLimitExceeded = errors.New("CEL cost limit exceeded")
)

// CELEvaluator provides CEL expression evaluation for saga scripts.
// It maintains a CEL environment with saga-specific variables and provides
// methods to compile and evaluate CEL expressions within the saga context.
type CELEvaluator struct {
	env       *cel.Env
	costLimit uint64
}

// NewCELEvaluator creates a CEL evaluator with saga-specific environment and the
// default runtime cost limit (DefaultCELCostLimit).
// The environment includes the following variables:
//   - input: map[string]any - saga input parameters
//   - ctx: map[string]string - saga context metadata (saga_execution_id, correlation_id)
func NewCELEvaluator() (*CELEvaluator, error) {
	return newCELEvaluator(DefaultCELCostLimit)
}

// newCELEvaluator builds a CEL evaluator bounded by the given runtime cost limit.
// A zero limit disables cost tracking (unbounded); callers should pass a positive
// value. It exists to let tests exercise the cost-limit boundary with small limits.
func newCELEvaluator(costLimit uint64) (*CELEvaluator, error) {
	env, err := cel.NewEnv(
		cel.Variable("input", cel.DynType),
		cel.Variable("ctx", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}
	return &CELEvaluator{env: env, costLimit: costLimit}, nil
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

	// Create program with a bounded runtime cost limit so a tenant expression
	// cannot run unboundedly expensive computation (bounded-execution guarantee).
	programOpts := make([]cel.ProgramOption, 0, 1)
	if e.costLimit > 0 {
		programOpts = append(programOpts, cel.CostLimit(e.costLimit))
	}
	prg, err := e.env.Program(ast, programOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program: %w", err)
	}

	// Evaluate
	result, _, err := prg.Eval(variables)
	if err != nil {
		if isCostLimitExceeded(err) {
			return nil, fmt.Errorf("%w: cost exceeded limit of %d", ErrCELCostLimitExceeded, e.costLimit)
		}
		return nil, fmt.Errorf("%w: %w", ErrCELEvaluationFailed, err)
	}

	// Convert CEL result to Go value
	return result.Value(), nil
}

// isCostLimitExceeded reports whether a CEL evaluation error was raised because
// the expression exceeded its configured runtime cost limit.
func isCostLimitExceeded(err error) bool {
	var cancelled interpreter.EvalCancelledError
	return errors.As(err, &cancelled) && cancelled.Cause == interpreter.CostLimitExceeded
}
