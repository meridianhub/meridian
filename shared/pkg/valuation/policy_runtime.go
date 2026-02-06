package valuation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
)

// CEL security constraints (aligned with reference-data/cel/compiler.go).
const (
	// MaxExpressionLength is the maximum allowed length of a CEL expression in bytes.
	MaxExpressionLength = 4096 // 4KB

	// MaxExpressionDepth is the maximum allowed nesting depth of a CEL expression.
	MaxExpressionDepth = 10

	// CostLimit is the maximum evaluation cost for a CEL program.
	// This prevents expensive expressions from consuming excessive resources.
	CostLimit = 10000
)

var (
	// ErrExpressionTooLong is returned when an expression exceeds MaxExpressionLength.
	ErrExpressionTooLong = errors.New("expression exceeds maximum length")

	// ErrExpressionTooDeep is returned when an expression exceeds MaxExpressionDepth.
	ErrExpressionTooDeep = errors.New("expression exceeds maximum nesting depth")

	// ErrCompilation is returned when CEL compilation fails.
	ErrCompilation = errors.New("CEL compilation failed")

	// ErrEvaluation is returned when CEL evaluation fails.
	ErrEvaluation = errors.New("CEL evaluation failed")

	// ErrInvalidPolicyType is returned when a policy has an unexpected type.
	ErrInvalidPolicyType = errors.New("invalid policy type")
)

// defaultPolicyRuntime implements PolicyRuntime using google/cel-go.
type defaultPolicyRuntime struct {
	env *cel.Env
}

// NewPolicyRuntime creates a new PolicyRuntime with a configured CEL environment.
// The environment supports standard CEL types and operations for valuation expressions.
func NewPolicyRuntime() (PolicyRuntime, error) {
	// Create CEL environment with standard declarations for valuation
	// Variables can be anything - the policy will declare what it needs
	env, err := cel.NewEnv(
		cel.Variable("amount", cel.DoubleType),
		cel.Variable("rate", cel.DoubleType),
		cel.Variable("tier", cel.StringType),
		cel.Variable("tariffs", cel.MapType(cel.StringType, cel.DoubleType)),
		cel.Variable("period", cel.StringType),
		cel.Variable("kwh", cel.DoubleType),
		cel.Variable("base_rate", cel.DoubleType),
		cel.Variable("volume_rate", cel.DoubleType),
		cel.Variable("quantity", cel.DoubleType),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &defaultPolicyRuntime{
		env: env,
	}, nil
}

// CompilePolicy compiles a CEL expression and validates it meets security constraints.
func (r *defaultPolicyRuntime) CompilePolicy(expression string) (CompiledPolicy, error) {
	// Validate expression constraints
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	// Compile the expression
	ast, issues := r.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrCompilation, issues.Err())
	}

	// Create program with cost limit
	prg, err := r.env.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, fmt.Errorf("%w: program creation failed: %w", ErrCompilation, err)
	}

	return &compiledPolicy{
		expression: expression,
		program:    prg,
	}, nil
}

// EvaluatePolicy executes a compiled policy with the given inputs.
func (r *defaultPolicyRuntime) EvaluatePolicy(
	_ context.Context,
	policy CompiledPolicy,
	inputs map[string]interface{},
) (interface{}, uint64, error) {
	cp, ok := policy.(*compiledPolicy)
	if !ok {
		return nil, 0, fmt.Errorf("%w: expected *compiledPolicy", ErrInvalidPolicyType)
	}

	// Evaluate the program
	result, details, err := cp.program.Eval(inputs)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrEvaluation, err)
	}

	// Check for CEL errors in result
	if types.IsError(result) {
		return nil, 0, fmt.Errorf("%w: %v", ErrEvaluation, result)
	}

	// Get actual cost from evaluation details
	var actualCost uint64
	if details != nil && details.ActualCost() != nil {
		actualCost = *details.ActualCost()
	} else {
		// Cost tracking not available - use conservative estimate
		actualCost = cp.EstimatedCost()
	}

	return result.Value(), actualCost, nil
}

// compiledPolicy implements CompiledPolicy.
type compiledPolicy struct {
	expression string
	program    cel.Program
}

// EstimatedCost returns the estimated cost in CEL units.
// For valuation policies, we use a conservative estimate that's well below
// the 10,000 unit limit to ensure we don't reject valid policies during compilation.
func (p *compiledPolicy) EstimatedCost() uint64 {
	// Conservative estimate for typical valuation expressions
	// Actual cost will be tracked during evaluation
	return 1000
}

// Expression returns the original CEL expression.
func (p *compiledPolicy) Expression() string {
	return p.expression
}

// validateExpressionConstraints checks that an expression meets security constraints.
func validateExpressionConstraints(expression string) error {
	if len(expression) > MaxExpressionLength {
		return fmt.Errorf("%w: length %d exceeds %d", ErrExpressionTooLong, len(expression), MaxExpressionLength)
	}

	depth := measureExpressionDepth(expression)
	if depth > MaxExpressionDepth {
		return fmt.Errorf("%w: depth %d exceeds %d", ErrExpressionTooDeep, depth, MaxExpressionDepth)
	}

	return nil
}

// measureExpressionDepth estimates the nesting depth of an expression.
// This is a heuristic based on parentheses and bracket nesting.
func measureExpressionDepth(expression string) int {
	maxDepth := 0
	currentDepth := 0

	for _, ch := range expression {
		switch ch {
		case '(', '[', '{':
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case ')', ']', '}':
			currentDepth--
			if currentDepth < 0 {
				currentDepth = 0
			}
		}
	}

	return maxDepth
}
