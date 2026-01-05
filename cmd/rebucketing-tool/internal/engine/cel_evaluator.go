package engine

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
)

// CELEvaluator wraps CEL compilation and evaluation for bucket key expressions.
// It provides a safe interface for evaluating fungibility key expressions
// against measurement attributes.
type CELEvaluator struct {
	compiler *refcel.Compiler
	mu       sync.RWMutex
	programs map[string]cel.Program // Cache compiled programs by expression
}

// NewCELEvaluator creates a new CEL evaluator using the reference-data CEL compiler.
func NewCELEvaluator() (*CELEvaluator, error) {
	compiler, err := refcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("create CEL compiler: %w", err)
	}

	return &CELEvaluator{
		compiler: compiler,
		programs: make(map[string]cel.Program),
	}, nil
}

// CompileBucketKeyExpression compiles a bucket key CEL expression.
// Returns ErrInvalidCELExpression if compilation fails.
// Compiled programs are cached for reuse.
func (e *CELEvaluator) CompileBucketKeyExpression(expression string) (cel.Program, error) {
	if expression == "" {
		return nil, fmt.Errorf("%w: expression cannot be empty", ErrInvalidCELExpression)
	}

	// Check cache first
	e.mu.RLock()
	if program, ok := e.programs[expression]; ok {
		e.mu.RUnlock()
		return program, nil
	}
	e.mu.RUnlock()

	// Compile the expression
	program, err := e.compiler.CompileBucketKey(expression)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidCELExpression, err)
	}

	// Cache the compiled program
	e.mu.Lock()
	e.programs[expression] = program
	e.mu.Unlock()

	return program, nil
}

// EvaluateBucketKey evaluates a compiled CEL program against the provided attributes.
// Returns the computed bucket key string or an error if evaluation fails.
func (e *CELEvaluator) EvaluateBucketKey(program cel.Program, attributes map[string]string) (string, error) {
	if program == nil {
		return "", fmt.Errorf("%w: program is nil", ErrCELEvaluation)
	}

	// Ensure attributes is not nil
	if attributes == nil {
		attributes = make(map[string]string)
	}

	// Build the activation (input variables)
	input := map[string]any{
		"attributes": attributes,
	}

	// Evaluate the program
	result, _, err := program.Eval(input)
	if err != nil {
		return "", fmt.Errorf("%w: evaluation error: %w", ErrCELEvaluation, err)
	}

	// Extract the string result
	bucketKey, ok := result.Value().(string)
	if !ok {
		return "", fmt.Errorf("%w: expression must return string, got %T", ErrCELEvaluation, result.Value())
	}

	return bucketKey, nil
}

// EvaluateBucketKeyWithExpression compiles and evaluates in one call.
// Uses cached programs when available.
func (e *CELEvaluator) EvaluateBucketKeyWithExpression(expression string, attributes map[string]string) (string, error) {
	program, err := e.CompileBucketKeyExpression(expression)
	if err != nil {
		return "", err
	}

	return e.EvaluateBucketKey(program, attributes)
}

// ValidateExpression checks if a CEL expression is valid without evaluating it.
// Returns nil if valid, ErrInvalidCELExpression otherwise.
func (e *CELEvaluator) ValidateExpression(expression string) error {
	_, err := e.CompileBucketKeyExpression(expression)
	return err
}

// ClearCache clears the compiled program cache.
// Useful for testing or when expressions are updated.
func (e *CELEvaluator) ClearCache() {
	e.mu.Lock()
	e.programs = make(map[string]cel.Program)
	e.mu.Unlock()
}

// CacheSize returns the number of cached compiled programs.
func (e *CELEvaluator) CacheSize() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.programs)
}
