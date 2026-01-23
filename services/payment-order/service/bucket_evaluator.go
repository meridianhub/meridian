// Package service implements gRPC services for the payment order domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// ErrBucketExpressionNull is returned when a bucket expression evaluates to null.
var ErrBucketExpressionNull = errors.New("bucket expression returned null")

// BucketEvaluator evaluates CEL expressions to compute bucket IDs for fungibility constraints.
// It caches compiled CEL programs for performance and thread-safety.
type BucketEvaluator struct {
	mu     sync.RWMutex
	cache  map[string]cel.Program
	env    *cel.Env
	logger *slog.Logger
}

// BucketEvalContext contains the context variables available to CEL expressions.
// These mirror the variables expected by instrument fungibility_key_expression.
type BucketEvalContext struct {
	// InstrumentCode is the payment instrument code (e.g., "RICE_V1", "USD").
	InstrumentCode string
	// Attributes is a map of instrument-specific attributes (e.g., {"grade": "A"}).
	Attributes map[string]string
}

// NewBucketEvaluator creates a new bucket evaluator with a CEL environment.
func NewBucketEvaluator(logger *slog.Logger) (*BucketEvaluator, error) {
	// Create CEL environment with standard library and variable declarations
	env, err := cel.NewEnv(
		cel.Variable("instrument_code", cel.StringType),
		cel.Variable("attributes", cel.MapType(cel.StringType, cel.StringType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL environment: %w", err)
	}

	return &BucketEvaluator{
		cache:  make(map[string]cel.Program),
		env:    env,
		logger: logger,
	}, nil
}

// Evaluate compiles and evaluates a CEL expression with the given context.
// Returns the bucket ID as a string. Returns empty string if expression is empty.
// Caches compiled programs for performance.
func (e *BucketEvaluator) Evaluate(_ context.Context, expression string, evalCtx BucketEvalContext) (string, error) {
	if expression == "" {
		// Empty expression means fully fungible - return empty bucket ID
		return "", nil
	}

	// Get or compile the program
	program, err := e.getOrCompile(expression)
	if err != nil {
		return "", fmt.Errorf("failed to compile CEL expression: %w", err)
	}

	// Build activation with context variables
	// Ensure attributes is non-nil to prevent CEL map access issues
	attrs := evalCtx.Attributes
	if attrs == nil {
		attrs = make(map[string]string)
	}
	activation := map[string]interface{}{
		"instrument_code": evalCtx.InstrumentCode,
		"attributes":      attrs,
	}

	// Evaluate the program
	result, _, err := program.Eval(activation)
	if err != nil {
		return "", fmt.Errorf("CEL evaluation failed: %w", err)
	}

	// Convert result to string
	bucketID, err := resultToString(result)
	if err != nil {
		return "", fmt.Errorf("CEL result conversion failed: %w", err)
	}

	e.logger.Debug("evaluated bucket ID",
		"expression", expression,
		"instrument_code", evalCtx.InstrumentCode,
		"attributes", evalCtx.Attributes,
		"bucket_id", bucketID)

	return bucketID, nil
}

// getOrCompile returns a cached program or compiles and caches a new one.
func (e *BucketEvaluator) getOrCompile(expression string) (cel.Program, error) {
	// Check cache first with read lock
	e.mu.RLock()
	if program, ok := e.cache[expression]; ok {
		e.mu.RUnlock()
		return program, nil
	}
	e.mu.RUnlock()

	// Compile with write lock
	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if program, ok := e.cache[expression]; ok {
		return program, nil
	}

	// Compile the expression
	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("CEL compilation error: %w", issues.Err())
	}

	// Create the program
	program, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program: %w", err)
	}

	// Cache and return
	e.cache[expression] = program
	return program, nil
}

// resultToString converts a CEL result to a string.
func resultToString(result ref.Val) (string, error) {
	switch v := result.(type) {
	case types.String:
		return string(v), nil
	case types.Int:
		return fmt.Sprintf("%d", int64(v)), nil
	case types.Double:
		return strconv.FormatFloat(float64(v), 'f', -1, 64), nil
	case types.Bool:
		return fmt.Sprintf("%t", bool(v)), nil
	default:
		// Try to convert to native and format as string
		native := result.Value()
		if native == nil {
			return "", ErrBucketExpressionNull
		}
		return fmt.Sprintf("%v", native), nil
	}
}
