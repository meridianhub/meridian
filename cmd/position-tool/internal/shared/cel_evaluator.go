package shared

import (
	"errors"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	celcore "github.com/meridianhub/meridian/services/reference-data/cel"
)

// CEL evaluator errors.
var (
	// ErrNilCompiler is returned when a nil compiler is passed.
	ErrNilCompiler = errors.New("CEL compiler cannot be nil")
	// ErrEmptyExpression is returned when an empty expression is provided.
	ErrEmptyExpression = errors.New("bucket key expression cannot be empty")
	// ErrEvaluationFailed is returned when CEL evaluation fails.
	ErrEvaluationFailed = errors.New("bucket key evaluation failed")
	// ErrInvalidResultType is returned when the evaluation result is not a string.
	ErrInvalidResultType = errors.New("bucket key expression must return a string")
)

// CELEvaluator wraps the reference-data CEL compiler for bucket key generation.
// It provides a thread-safe, reusable evaluator for bulk import operations.
//
// The evaluator caches compiled programs for the same expression, avoiding
// repeated compilation overhead during batch processing.
type CELEvaluator struct {
	compiler *celcore.Compiler

	// Cache for compiled programs
	mu       sync.RWMutex
	programs map[string]cel.Program
}

// NewCELEvaluator creates a new CEL evaluator using the reference-data compiler.
// Returns ErrNilCompiler if the compiler is nil.
func NewCELEvaluator(compiler *celcore.Compiler) (*CELEvaluator, error) {
	if compiler == nil {
		return nil, ErrNilCompiler
	}
	return &CELEvaluator{
		compiler: compiler,
		programs: make(map[string]cel.Program),
	}, nil
}

// NewCELEvaluatorDefault creates a new CEL evaluator with a default compiler.
// This is a convenience constructor for typical usage.
func NewCELEvaluatorDefault() (*CELEvaluator, error) {
	compiler, err := celcore.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}
	return NewCELEvaluator(compiler)
}

// EvaluateBucketKey evaluates a bucket key expression against the given attributes.
// The expression must return a string value (typically using the bucket_key() function).
//
// Parameters:
//   - expression: CEL expression for bucket key generation (e.g., `bucket_key([attributes["region"], attributes["type"]])`)
//   - attributes: Map of attribute key-value pairs available as the `attributes` variable in CEL
//
// Returns:
//   - The evaluated bucket key as a string
//   - An error if compilation or evaluation fails
//
// Thread-safety: This method is safe for concurrent use. Compiled programs are cached
// per expression to optimize bulk operations.
func (e *CELEvaluator) EvaluateBucketKey(expression string, attributes map[string]string) (string, error) {
	if expression == "" {
		return "", ErrEmptyExpression
	}

	program, err := e.getOrCompile(expression)
	if err != nil {
		return "", err
	}

	// Evaluate with the attributes map
	result, _, err := program.Eval(map[string]any{
		"attributes": attributes,
	})
	if err != nil {
		return "", errors.Join(ErrEvaluationFailed, err)
	}

	// Extract string result
	bucketKey, ok := result.Value().(string)
	if !ok {
		return "", fmt.Errorf("%w: got %T", ErrInvalidResultType, result.Value())
	}

	return bucketKey, nil
}

// getOrCompile retrieves a cached compiled program or compiles a new one.
func (e *CELEvaluator) getOrCompile(expression string) (cel.Program, error) {
	// Fast path: check cache with read lock
	e.mu.RLock()
	if program, ok := e.programs[expression]; ok {
		e.mu.RUnlock()
		return program, nil
	}
	e.mu.RUnlock()

	// Slow path: compile and cache with write lock
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if program, ok := e.programs[expression]; ok {
		return program, nil
	}

	// Compile the expression
	program, err := e.compiler.CompileBucketKey(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile bucket key expression: %w", err)
	}

	e.programs[expression] = program
	return program, nil
}

// CachedExpressionCount returns the number of cached compiled expressions.
// This is useful for monitoring and testing.
func (e *CELEvaluator) CachedExpressionCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.programs)
}

// ClearCache clears all cached compiled expressions.
// This is useful for testing or when expressions need to be recompiled.
func (e *CELEvaluator) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.programs = make(map[string]cel.Program)
}
