// Package service provides the business logic for the Market Information service.
package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/shopspring/decimal"

	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
)

// Security constraints for CEL expressions per ADR-0017.
const (
	// MaxExpressionLength is the maximum allowed length of a CEL expression in bytes.
	MaxExpressionLength = 4096

	// MaxExpressionDepth is the maximum allowed nesting depth of a CEL expression.
	MaxExpressionDepth = 10

	// CostLimit is the maximum evaluation cost for a CEL program.
	CostLimit = 10000
)

// Error types for CEL operations.
var (
	// ErrExpressionTooLong is returned when an expression exceeds MaxExpressionLength.
	ErrExpressionTooLong = errors.New("expression exceeds maximum length")

	// ErrExpressionTooDeep is returned when an expression exceeds MaxExpressionDepth.
	ErrExpressionTooDeep = errors.New("expression exceeds maximum nesting depth")

	// ErrEnvironmentCreation is returned when a CEL environment cannot be created.
	ErrEnvironmentCreation = errors.New("failed to create CEL environment")

	// ErrCompilation is returned when CEL compilation fails.
	ErrCompilation = errors.New("CEL compilation failed")

	// ErrEvaluation is returned when CEL evaluation fails.
	ErrEvaluation = errors.New("CEL evaluation failed")

	// ErrInvalidDecimal is returned when a string cannot be parsed as a decimal.
	ErrInvalidDecimal = errors.New("invalid decimal value")
)

// CelValidator provides CEL expression compilation and evaluation for market data validation.
// It maintains three separate environments for different expression types:
// - Validation: For validating observation values
// - Resolution Key: For generating unique keys from observation context
// - Error Message: For generating custom validation error messages
//
// Compiled programs are cached for performance. Thread-safe.
type CelValidator struct {
	validationEnv    *cel.Env
	resolutionKeyEnv *cel.Env
	errorMessageEnv  *cel.Env

	// Cache for compiled programs, keyed by expression string
	validationCache    map[string]cel.Program
	resolutionKeyCache map[string]cel.Program
	errorMessageCache  map[string]cel.Program

	// Mutex for thread-safe cache access
	validationMu    sync.RWMutex
	resolutionKeyMu sync.RWMutex
	errorMessageMu  sync.RWMutex
}

// NewCelValidator creates a new CelValidator with configured environments.
func NewCelValidator() (*CelValidator, error) {
	validationEnv, err := createValidationEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("validation env: %w", err))
	}

	resolutionKeyEnv, err := createResolutionKeyEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("resolution key env: %w", err))
	}

	errorMessageEnv, err := createErrorMessageEnv()
	if err != nil {
		return nil, errors.Join(ErrEnvironmentCreation, fmt.Errorf("error message env: %w", err))
	}

	return &CelValidator{
		validationEnv:      validationEnv,
		resolutionKeyEnv:   resolutionKeyEnv,
		errorMessageEnv:    errorMessageEnv,
		validationCache:    make(map[string]cel.Program),
		resolutionKeyCache: make(map[string]cel.Program),
		errorMessageCache:  make(map[string]cel.Program),
	}, nil
}

// createValidationEnv creates the CEL environment for validation expressions.
// Variables available:
//   - value: string - the observation value as a string
//   - observation_context: map[string]string - key-value attributes from the observation
//   - observed_at: timestamp - when the observation was made
//   - valid_from: timestamp - start of effective time range
//   - valid_to: timestamp - end of effective time range
//   - source_id: string - the data source identifier
//   - quality: int - confidence grade on the proto QualityLevel scale
//     (1=Estimate, 2=Provisional, 3=Actual, 4=Verified). This is the proto enum
//     value (int(req.Quality)), which cut over to the four-level ladder in #2248.
//     CEL rules comparing quality to an integer literal must account for the new
//     intermediate PROVISIONAL=2 (e.g. `quality >= 2` now includes Provisional).
func createValidationEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("value", cel.StringType),
		cel.Variable("observation_context", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("observed_at", cel.TimestampType),
		cel.Variable("valid_from", cel.TimestampType),
		cel.Variable("valid_to", cel.TimestampType),
		cel.Variable("source_id", cel.StringType),
		cel.Variable("quality", cel.IntType),
		DecimalLib(),
	)
}

// createResolutionKeyEnv creates the CEL environment for resolution key expressions.
// Variables available:
//   - observation_context: map[string]string - key-value attributes from the observation
//
// The expression must return a string representing the resolution key.
// Example: has(observation_context.base_code) && has(observation_context.quote_code)
//
//	? observation_context.base_code + '/' + observation_context.quote_code
//	: 'UNKNOWN'
func createResolutionKeyEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("observation_context", cel.MapType(cel.StringType, cel.StringType)),
	)
}

// createErrorMessageEnv creates the CEL environment for error message expressions.
// Variables available:
//   - value: string - the observation value as a string
//   - observation_context: map[string]string - key-value attributes from the observation
//   - dataset_code: string - the dataset code for context in error messages
//
// The expression must return a string representing the error message.
func createErrorMessageEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("value", cel.StringType),
		cel.Variable("observation_context", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("dataset_code", cel.StringType),
	)
}

// CompileValidation compiles a validation expression and caches the result.
// Returns the compiled program that can be evaluated with validation context.
// Uses double-checked locking to prevent redundant compilation when multiple
// goroutines request the same expression concurrently.
func (v *CelValidator) CompileValidation(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	// Fast path: check cache with read lock
	v.validationMu.RLock()
	if prg, ok := v.validationCache[expression]; ok {
		v.validationMu.RUnlock()
		return prg, nil
	}
	v.validationMu.RUnlock()

	// Slow path: acquire write lock and double-check
	v.validationMu.Lock()
	defer v.validationMu.Unlock()

	// Double-check: another goroutine may have compiled while we waited for the lock
	if prg, ok := v.validationCache[expression]; ok {
		return prg, nil
	}

	// Compile the expression
	ast, issues := v.validationEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	prg, err := v.validationEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	// Cache the compiled program
	v.validationCache[expression] = prg

	return prg, nil
}

// CompileResolutionKey compiles a resolution key expression and caches the result.
// Returns the compiled program that can be evaluated with observation context.
// Uses double-checked locking to prevent redundant compilation when multiple
// goroutines request the same expression concurrently.
func (v *CelValidator) CompileResolutionKey(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	// Fast path: check cache with read lock
	v.resolutionKeyMu.RLock()
	if prg, ok := v.resolutionKeyCache[expression]; ok {
		v.resolutionKeyMu.RUnlock()
		return prg, nil
	}
	v.resolutionKeyMu.RUnlock()

	// Slow path: acquire write lock and double-check
	v.resolutionKeyMu.Lock()
	defer v.resolutionKeyMu.Unlock()

	// Double-check: another goroutine may have compiled while we waited for the lock
	if prg, ok := v.resolutionKeyCache[expression]; ok {
		return prg, nil
	}

	// Compile the expression
	ast, issues := v.resolutionKeyEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	prg, err := v.resolutionKeyEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	// Cache the compiled program
	v.resolutionKeyCache[expression] = prg

	return prg, nil
}

// CompileErrorMessage compiles an error message expression and caches the result.
// Returns the compiled program that can be evaluated with error context.
// Uses double-checked locking to prevent redundant compilation when multiple
// goroutines request the same expression concurrently.
func (v *CelValidator) CompileErrorMessage(expression string) (cel.Program, error) {
	if err := validateExpressionConstraints(expression); err != nil {
		return nil, err
	}

	// Fast path: check cache with read lock
	v.errorMessageMu.RLock()
	if prg, ok := v.errorMessageCache[expression]; ok {
		v.errorMessageMu.RUnlock()
		return prg, nil
	}
	v.errorMessageMu.RUnlock()

	// Slow path: acquire write lock and double-check
	v.errorMessageMu.Lock()
	defer v.errorMessageMu.Unlock()

	// Double-check: another goroutine may have compiled while we waited for the lock
	if prg, ok := v.errorMessageCache[expression]; ok {
		return prg, nil
	}

	// Compile the expression
	ast, issues := v.errorMessageEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, errors.Join(ErrCompilation, issues.Err())
	}

	prg, err := v.errorMessageEnv.Program(ast, cel.CostLimit(CostLimit))
	if err != nil {
		return nil, errors.Join(ErrCompilation, err)
	}

	// Cache the compiled program
	v.errorMessageCache[expression] = prg

	return prg, nil
}

// ValidationInput represents the input for a validation expression evaluation.
type ValidationInput struct {
	Value              string
	ObservationContext map[string]string
	ObservedAt         time.Time
	ValidFrom          time.Time
	ValidTo            time.Time
	SourceID           string
	Quality            int
}

// EvaluateValidation evaluates a validation expression with the given input.
// Returns true if the validation passes, false otherwise.
// If evaluation fails, returns an error.
func (v *CelValidator) EvaluateValidation(prg cel.Program, input ValidationInput) (bool, error) {
	result, _, err := prg.Eval(map[string]interface{}{
		"value":               input.Value,
		"observation_context": input.ObservationContext,
		"observed_at":         input.ObservedAt,
		"valid_from":          input.ValidFrom,
		"valid_to":            input.ValidTo,
		"source_id":           input.SourceID,
		"quality":             input.Quality,
	})
	if err != nil {
		return false, errors.Join(ErrEvaluation, err)
	}

	boolResult, ok := result.Value().(bool)
	if !ok {
		return false, fmt.Errorf("%w: expected bool, got %T", ErrEvaluation, result.Value())
	}

	return boolResult, nil
}

// ResolutionKeyInput represents the input for a resolution key expression evaluation.
type ResolutionKeyInput struct {
	ObservationContext map[string]string
}

// EvaluateResolutionKey evaluates a resolution key expression with the given input.
// Returns the computed resolution key string.
func (v *CelValidator) EvaluateResolutionKey(prg cel.Program, input ResolutionKeyInput) (string, error) {
	result, _, err := prg.Eval(map[string]interface{}{
		"observation_context": input.ObservationContext,
	})
	if err != nil {
		return "", errors.Join(ErrEvaluation, err)
	}

	strResult, ok := result.Value().(string)
	if !ok {
		return "", fmt.Errorf("%w: expected string, got %T", ErrEvaluation, result.Value())
	}

	return strResult, nil
}

// ErrorMessageInput represents the input for an error message expression evaluation.
type ErrorMessageInput struct {
	Value              string
	ObservationContext map[string]string
	DatasetCode        string
}

// EvaluateErrorMessage evaluates an error message expression with the given input.
// Returns the computed error message string.
func (v *CelValidator) EvaluateErrorMessage(prg cel.Program, input ErrorMessageInput) (string, error) {
	result, _, err := prg.Eval(map[string]interface{}{
		"value":               input.Value,
		"observation_context": input.ObservationContext,
		"dataset_code":        input.DatasetCode,
	})
	if err != nil {
		return "", errors.Join(ErrEvaluation, err)
	}

	strResult, ok := result.Value().(string)
	if !ok {
		return "", fmt.Errorf("%w: expected string, got %T", ErrEvaluation, result.Value())
	}

	return strResult, nil
}

// ToContextMap converts a slice of proto AttributeEntry to a map for CEL evaluation.
// Returns an empty map if entries is nil.
func ToContextMap(entries []*quantityv1.AttributeEntry) map[string]string {
	if entries == nil {
		return make(map[string]string)
	}

	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry != nil {
			result[entry.Key] = entry.Value
		}
	}
	return result
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
// It ignores brackets inside string literals to avoid false positives.
func measureExpressionDepth(expression string) int { //nolint:gocognit // pre-existing, tracked in assess-2026-05-22
	maxDepth := 0
	currentDepth := 0
	inString := false
	var stringChar rune
	prevEscape := false

	for _, ch := range expression {
		// Handle escape sequences in strings
		if inString {
			if prevEscape {
				prevEscape = false
				continue
			}
			if ch == '\\' {
				prevEscape = true
				continue
			}
			if ch == stringChar {
				inString = false
			}
			continue
		}

		// Detect string start
		if ch == '"' || ch == '\'' || ch == '`' {
			inString = true
			stringChar = ch
			continue
		}

		// Only count brackets outside of strings
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

// DecimalLib creates a CEL function library for decimal parsing using shopspring/decimal.
//
// Functions:
//   - decimal(string) -> double: Parses a string to a decimal and returns as double for comparison
func DecimalLib() cel.EnvOption {
	return cel.Lib(&decimalLibrary{})
}

type decimalLibrary struct{}

func (*decimalLibrary) LibraryName() string {
	return "meridian.Decimal"
}

func (*decimalLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("decimal",
			cel.Overload("decimal_string",
				[]*cel.Type{cel.StringType},
				cel.DoubleType,
				cel.UnaryBinding(parseDecimalValue),
			),
		),
	}
}

func (*decimalLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

// parseDecimalValue parses a string to a decimal using shopspring/decimal
// and returns as CEL Double for comparison operations.
func parseDecimalValue(val ref.Val) ref.Val {
	s, ok := val.Value().(string)
	if !ok {
		return types.NewErr("decimal: expected string, got %T", val.Value())
	}

	d, err := decimal.NewFromString(s)
	if err != nil {
		return types.NewErr("decimal: invalid decimal value %q: %v", s, err)
	}

	// Convert to float64 for CEL Double type.
	// IEEE-754 float64 provides ~15-17 significant decimal digits, which is
	// sufficient for typical market data (FX rates: 4-6 decimals, prices: 2-8 decimals).
	// The FIX protocol standard specifies float fields must accommodate up to
	// 15 significant digits, confirming this precision is industry-standard.
	// For assets requiring exact precision (accounting, settlement), use
	// integer arithmetic in application code rather than CEL validation.
	f, exact := d.Float64()
	if !exact {
		// Precision loss occurred - this is expected for values with more than
		// ~15 significant digits. For market data validation purposes, this
		// level of precision is acceptable. If exact precision is required,
		// the application layer should use decimal arithmetic directly.
		_ = exact // Acknowledge we're ignoring the exactness flag intentionally
	}
	return types.Double(f)
}
