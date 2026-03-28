// Package service provides fungibility validation for double-entry transactions.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// Fungibility validation errors.
var (
	// ErrFungibilityMismatch is returned when debit and credit postings have attributes
	// that evaluate to different fungibility keys.
	ErrFungibilityMismatch = errors.New("fungibility validation failed: debit and credit have incompatible attributes")

	// ErrFungibilityKeyEvaluation is returned when the CEL program fails to evaluate
	// the fungibility key expression.
	ErrFungibilityKeyEvaluation = errors.New("failed to evaluate fungibility key expression")

	// ErrInstrumentNotFound is returned when the instrument cannot be found in the
	// reference data service.
	ErrInstrumentNotFound = errors.New("instrument not found")

	// ErrFungibilityKeyType is returned when the fungibility key expression returns
	// a non-string type.
	ErrFungibilityKeyType = errors.New("fungibility key expression must return string")
)

// InstrumentGetter retrieves instrument definitions with pre-compiled CEL programs.
// This interface is implemented by the reference-data client.
type InstrumentGetter interface {
	GetInstrument(ctx context.Context, code string, version int) (*cache.CachedInstrument, error)
}

// FungibilityKeyEvaluator evaluates fungibility key expressions.
// This interface enables testing without depending on the actual CEL runtime.
type FungibilityKeyEvaluator interface {
	Eval(activation interface{}) (interface{}, error)
}

// FungibilityValidator validates that debit and credit postings have compatible
// fungibility attributes as defined by the instrument's fungibility_key_expression.
//
// Thread-safety: Safe for concurrent use.
type FungibilityValidator struct {
	getter    InstrumentGetter
	evaluator FungibilityKeyEvaluator // Optional: for testing only. If nil, uses instrument's BucketKeyProgram.
}

// NewFungibilityValidator creates a new fungibility validator.
// Returns nil if getter is nil.
func NewFungibilityValidator(getter InstrumentGetter) *FungibilityValidator {
	if getter == nil {
		return nil
	}
	return &FungibilityValidator{
		getter: getter,
	}
}

// ValidateDoubleEntry validates that debit and credit attributes are compatible
// according to the instrument's fungibility rules.
//
// Parameters:
//   - ctx: Context with tenant information
//   - instrumentCode: The instrument code (e.g., "USD", "RICE-KG")
//   - instrumentVersion: The instrument version (use 1 for latest active)
//   - debitAttrs: Attributes from the debit posting (may be nil)
//   - creditAttrs: Attributes from the credit posting (may be nil)
//
// Returns:
//   - nil if fungibility validation passes (keys match or instrument is fully fungible)
//   - ErrInstrumentNotFound if instrument cannot be found
//   - ErrFungibilityMismatch if keys don't match
//   - ErrFungibilityKeyEvaluation if CEL evaluation fails
func (v *FungibilityValidator) ValidateDoubleEntry(
	ctx context.Context,
	instrumentCode string,
	instrumentVersion int,
	debitAttrs map[string]string,
	creditAttrs map[string]string,
) error {
	// Retrieve instrument and resolve evaluator
	evaluator, err := v.resolveEvaluator(ctx, instrumentCode, instrumentVersion)
	if err != nil {
		return err
	}
	if evaluator == nil {
		// Fully fungible - no validation needed
		return nil
	}

	// Normalize nil attributes to empty maps for CEL evaluation
	if debitAttrs == nil {
		debitAttrs = make(map[string]string)
	}
	if creditAttrs == nil {
		creditAttrs = make(map[string]string)
	}

	// Evaluate and compare fungibility keys
	return compareFungibilityKeys(evaluator, debitAttrs, creditAttrs, instrumentCode)
}

// resolveEvaluator retrieves the instrument and determines the appropriate fungibility key evaluator.
// Returns nil evaluator (not an error) when the instrument is fully fungible.
func (v *FungibilityValidator) resolveEvaluator(ctx context.Context, instrumentCode string, instrumentVersion int) (FungibilityKeyEvaluator, error) {
	instrument, err := v.getter.GetInstrument(ctx, instrumentCode, instrumentVersion)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s v%d", ErrInstrumentNotFound, instrumentCode, instrumentVersion)
		}
		return nil, fmt.Errorf("failed to retrieve instrument %s: %w", instrumentCode, err)
	}

	if instrument.Definition.FungibilityKeyExpression == "" {
		return nil, nil
	}

	if v.evaluator != nil {
		return v.evaluator, nil
	}
	if instrument.BucketKeyProgram != nil {
		return &celProgramAdapter{program: instrument.BucketKeyProgram}, nil
	}
	return nil, nil
}

// compareFungibilityKeys evaluates the fungibility key for both debit and credit and compares them.
func compareFungibilityKeys(evaluator FungibilityKeyEvaluator, debitAttrs, creditAttrs map[string]string, instrumentCode string) error {
	debitKey, err := evaluateFungibilityKey(evaluator, debitAttrs)
	if err != nil {
		return fmt.Errorf("%w: debit attributes: %w", ErrFungibilityKeyEvaluation, err)
	}

	creditKey, err := evaluateFungibilityKey(evaluator, creditAttrs)
	if err != nil {
		return fmt.Errorf("%w: credit attributes: %w", ErrFungibilityKeyEvaluation, err)
	}

	if debitKey != creditKey {
		return fmt.Errorf("%w: debit key %q does not match credit key %q (instrument=%s)",
			ErrFungibilityMismatch,
			debitKey,
			creditKey,
			instrumentCode)
	}

	return nil
}

// celProgramAdapter wraps a cel.Program to implement FungibilityKeyEvaluator.
type celProgramAdapter struct {
	program cel.Program
}

// Eval evaluates the CEL program with the given activation.
func (a *celProgramAdapter) Eval(activation interface{}) (interface{}, error) {
	result, _, err := a.program.Eval(activation)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result.Value(), nil
	}
	return "", nil
}

// evaluateFungibilityKey evaluates the evaluator with the given attributes
// and returns the resulting fungibility key string.
func evaluateFungibilityKey(evaluator FungibilityKeyEvaluator, attributes map[string]string) (string, error) {
	// Build CEL activation with attributes variable
	activation := map[string]interface{}{
		"attributes": attributes,
	}

	result, err := evaluator.Eval(activation)
	if err != nil {
		return "", err
	}

	// Extract string value from result
	switch v := result.(type) {
	case string:
		return v, nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("%w: got %T", ErrFungibilityKeyType, result)
	}
}
