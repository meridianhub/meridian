package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// Test sentinel errors for wrapping.
var (
	errFungibilityTestConnRefused = errors.New("connection refused")
	errFungibilityTestCELEval     = errors.New("CEL evaluation failed")
)

// mockInstrumentGetter is a mock implementation of InstrumentGetter for testing.
type mockInstrumentGetter struct {
	instruments map[string]*cache.CachedInstrument
	err         error
}

func (m *mockInstrumentGetter) GetInstrument(_ context.Context, code string, _ int) (*cache.CachedInstrument, error) {
	if m.err != nil {
		return nil, m.err
	}
	key := code
	if inst, ok := m.instruments[key]; ok {
		return inst, nil
	}
	return nil, registry.ErrNotFound
}

// mockFungibilityKeyProgram implements a simple mock for fungibility key evaluation.
// This returns a deterministic key based on the attributes passed in.
type mockFungibilityKeyProgram struct {
	keyFunc func(attrs map[string]string) string
	err     error
}

// Eval evaluates the mock fungibility key program.
func (m *mockFungibilityKeyProgram) Eval(activation interface{}) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Extract attributes from activation map
	if act, ok := activation.(map[string]interface{}); ok {
		if attrs, ok := act["attributes"].(map[string]string); ok {
			return m.keyFunc(attrs), nil
		}
	}
	// Return empty string for missing/invalid attributes
	return m.keyFunc(map[string]string{}), nil
}

func TestFungibilityValidator_ValidateDoubleEntry_FullyFungible(t *testing.T) {
	// Instrument with empty fungibility_key_expression is fully fungible
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"USD": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "USD",
					Version:                  1,
					FungibilityKeyExpression: "", // Empty = fully fungible
				},
				BucketKeyProgram: nil, // No CEL program for fully fungible
			},
		},
	}

	validator := NewFungibilityValidator(mock)

	err := validator.ValidateDoubleEntry(ctx, "USD", 1, nil, nil)
	assert.NoError(t, err, "fully fungible instrument should pass validation")
}

func TestFungibilityValidator_ValidateDoubleEntry_MatchingKeys(t *testing.T) {
	// Instrument with fungibility_key_expression where attributes produce matching keys
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"RICE-KG": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "RICE-KG",
					Version:                  1,
					FungibilityKeyExpression: "attributes.batch_id",
				},
				// Note: BucketKeyProgram is nil for test - we override via FungibilityKeyEvaluator
			},
		},
	}

	// Use a validator with a custom evaluator for testing
	validator := &FungibilityValidator{
		getter: mock,
		evaluator: &mockFungibilityKeyProgram{
			keyFunc: func(attrs map[string]string) string {
				return attrs["batch_id"]
			},
		},
	}

	debitAttrs := map[string]string{"batch_id": "2024-A"}
	creditAttrs := map[string]string{"batch_id": "2024-A"}

	err := validator.ValidateDoubleEntry(ctx, "RICE-KG", 1, debitAttrs, creditAttrs)
	assert.NoError(t, err, "matching fungibility keys should pass validation")
}

func TestFungibilityValidator_ValidateDoubleEntry_MismatchedKeys(t *testing.T) {
	// Instrument with fungibility_key_expression where attributes produce different keys
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"RICE-KG": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "RICE-KG",
					Version:                  1,
					FungibilityKeyExpression: "attributes.batch_id",
				},
			},
		},
	}

	validator := &FungibilityValidator{
		getter: mock,
		evaluator: &mockFungibilityKeyProgram{
			keyFunc: func(attrs map[string]string) string {
				return attrs["batch_id"]
			},
		},
	}

	debitAttrs := map[string]string{"batch_id": "2024-A"}
	creditAttrs := map[string]string{"batch_id": "2024-B"} // Different batch_id

	err := validator.ValidateDoubleEntry(ctx, "RICE-KG", 1, debitAttrs, creditAttrs)
	assert.Error(t, err, "mismatched fungibility keys should fail validation")
	assert.ErrorIs(t, err, ErrFungibilityMismatch)
}

func TestFungibilityValidator_ValidateDoubleEntry_InstrumentNotFound(t *testing.T) {
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{}, // Empty - no instruments
	}

	validator := NewFungibilityValidator(mock)

	err := validator.ValidateDoubleEntry(ctx, "UNKNOWN", 1, nil, nil)
	assert.Error(t, err, "unknown instrument should fail validation")
	assert.ErrorIs(t, err, ErrInstrumentNotFound)
}

func TestFungibilityValidator_ValidateDoubleEntry_InstrumentLookupError(t *testing.T) {
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		err: fmt.Errorf("lookup failed: %w", errFungibilityTestConnRefused),
	}

	validator := NewFungibilityValidator(mock)

	err := validator.ValidateDoubleEntry(ctx, "USD", 1, nil, nil)
	assert.Error(t, err, "instrument lookup error should fail validation")
	assert.Contains(t, err.Error(), "connection refused")
}

func TestFungibilityValidator_ValidateDoubleEntry_NilAttributes(t *testing.T) {
	// Test that nil attributes are handled gracefully
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"RICE-KG": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "RICE-KG",
					Version:                  1,
					FungibilityKeyExpression: "attributes.batch_id",
				},
			},
		},
	}

	validator := &FungibilityValidator{
		getter: mock,
		evaluator: &mockFungibilityKeyProgram{
			keyFunc: func(attrs map[string]string) string {
				return attrs["batch_id"] // Returns empty for nil/empty attrs
			},
		},
	}

	// Both nil - should produce same (empty) key
	err := validator.ValidateDoubleEntry(ctx, "RICE-KG", 1, nil, nil)
	assert.NoError(t, err, "both nil attributes should produce matching empty keys")
}

func TestFungibilityValidator_ValidateDoubleEntry_CELEvaluationError(t *testing.T) {
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"RICE-KG": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "RICE-KG",
					Version:                  1,
					FungibilityKeyExpression: "bad_expression",
				},
			},
		},
	}

	validator := &FungibilityValidator{
		getter: mock,
		evaluator: &mockFungibilityKeyProgram{
			err: fmt.Errorf("expression error: %w", errFungibilityTestCELEval),
		},
	}

	err := validator.ValidateDoubleEntry(ctx, "RICE-KG", 1, nil, nil)
	assert.Error(t, err, "CEL evaluation error should fail validation")
	assert.ErrorIs(t, err, ErrFungibilityKeyEvaluation)
}

func TestFungibilityValidator_NilGetter(t *testing.T) {
	validator := NewFungibilityValidator(nil)
	assert.Nil(t, validator, "nil getter should return nil validator")
}

func TestNewFungibilityValidator(t *testing.T) {
	mock := &mockInstrumentGetter{}
	validator := NewFungibilityValidator(mock)
	require.NotNil(t, validator)
}

func TestFungibilityValidator_UseCELProgramFromInstrument(t *testing.T) {
	// Test that when no custom evaluator is set, the validator uses the instrument's BucketKeyProgram
	ctx := testdb.ContextWithTenant(t, "test-tenant")

	// This test verifies the production path works when BucketKeyProgram is nil (fully fungible)
	mock := &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"USD": {
				Definition: &registry.InstrumentDefinition{
					Code:                     "USD",
					Version:                  1,
					FungibilityKeyExpression: "", // Fully fungible
				},
				BucketKeyProgram: nil,
			},
		},
	}

	validator := NewFungibilityValidator(mock)

	// Should pass because instrument is fully fungible (no BucketKeyProgram)
	err := validator.ValidateDoubleEntry(ctx, "USD", 1,
		map[string]string{"any": "attr"},
		map[string]string{"different": "attr"},
	)
	assert.NoError(t, err)
}
