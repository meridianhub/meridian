package generator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock implementations ---

// mockValidator implements generator.ManifestValidator for tests.
type mockValidator struct {
	// responses is a queue of results to return on successive calls.
	// Each call pops the first response.
	responses []*generator.ValidationResult
	errors    []error
}

func (m *mockValidator) ValidateDryRun(_ context.Context, _ string) (*generator.ValidationResult, error) {
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(m.responses) == 0 {
		return &generator.ValidationResult{Valid: true}, nil
	}
	r := m.responses[0]
	m.responses = m.responses[1:]
	return r, nil
}

// mockLLMClient implements generator.LLMClient for tests.
type mockLLMClient struct {
	// fixResponses is a queue of manifests to return on Fix calls.
	fixResponses []string
	fixErrors    []error
	fixCallCount int
}

func (m *mockLLMClient) Generate(_ context.Context, _ string) (string, error) {
	return "", errors.New("Generate not expected in validate-fix tests")
}

func (m *mockLLMClient) Fix(_ context.Context, _ string, _ []generator.ValidationError) (string, error) {
	m.fixCallCount++
	if len(m.fixErrors) > 0 {
		err := m.fixErrors[0]
		m.fixErrors = m.fixErrors[1:]
		if err != nil {
			return "", err
		}
	}
	if len(m.fixResponses) == 0 {
		return "fixed: manifest", nil
	}
	r := m.fixResponses[0]
	m.fixResponses = m.fixResponses[1:]
	return r, nil
}

// --- ValidateAndFix tests ---

// TestValidateAndFix_ValidOnFirstPass checks that no LLM calls are made when
// the manifest passes validation immediately.
func TestValidateAndFix_ValidOnFirstPass(t *testing.T) {
	ctx := context.Background()

	validator := &mockValidator{
		responses: []*generator.ValidationResult{
			{Valid: true, Warnings: []generator.ValidationError{{Code: "WARN", Message: "minor issue"}}},
		},
	}
	llm := &mockLLMClient{}

	result, err := generator.ValidateAndFix(ctx, "instruments: []", generator.ValidateFixOptions{
		MaxIterations: 3,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, 0, result.IterationsUsed)
	assert.Equal(t, 0, llm.fixCallCount)
	assert.Len(t, result.Warnings, 1)
	assert.Len(t, result.Errors, 0)
	assert.Equal(t, "instruments: []", result.FinalManifest)
}

// TestValidateAndFix_FixOnFirstIteration checks that one LLM call fixes the manifest.
func TestValidateAndFix_FixOnFirstIteration(t *testing.T) {
	ctx := context.Background()

	const fixedYAML = "instruments:\n  - code: GBP"

	validator := &mockValidator{
		responses: []*generator.ValidationResult{
			// First call: errors
			{Valid: false, Errors: []generator.ValidationError{
				{Code: "MISSING_FIELD", Path: "instruments[0].code", Message: "code is required"},
			}},
			// Second call: valid
			{Valid: true},
		},
	}
	llm := &mockLLMClient{
		fixResponses: []string{fixedYAML},
	}

	result, err := generator.ValidateAndFix(ctx, "instruments:\n  - name: GBP", generator.ValidateFixOptions{
		MaxIterations: 3,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, 1, result.IterationsUsed)
	assert.Equal(t, 1, llm.fixCallCount)
	assert.Equal(t, fixedYAML, result.FinalManifest)
}

// TestValidateAndFix_MaxIterationsExceeded checks that the loop stops after MaxIterations
// and returns the remaining errors with Valid=false.
func TestValidateAndFix_MaxIterationsExceeded(t *testing.T) {
	ctx := context.Background()

	errResult := &generator.ValidationResult{
		Valid: false,
		Errors: []generator.ValidationError{
			{Code: "PERSISTENT_ERROR", Path: "instruments", Message: "still broken"},
		},
	}

	// Always return errors
	validator := &mockValidator{
		responses: []*generator.ValidationResult{errResult, errResult, errResult, errResult},
	}
	llm := &mockLLMClient{
		fixResponses: []string{"attempt1", "attempt2", "attempt3"},
	}

	result, err := generator.ValidateAndFix(ctx, "broken: manifest", generator.ValidateFixOptions{
		MaxIterations: 3,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Equal(t, 3, result.IterationsUsed)
	assert.Equal(t, 3, llm.fixCallCount)
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, "PERSISTENT_ERROR", result.Errors[0].Code)
}

// TestValidateAndFix_ZeroMaxIterations validates without attempting any LLM fixes.
func TestValidateAndFix_ZeroMaxIterations(t *testing.T) {
	ctx := context.Background()

	validator := &mockValidator{
		responses: []*generator.ValidationResult{
			{Valid: false, Errors: []generator.ValidationError{
				{Code: "ERR", Path: "x", Message: "broken"},
			}},
		},
	}
	llm := &mockLLMClient{}

	result, err := generator.ValidateAndFix(ctx, "broken: yes", generator.ValidateFixOptions{
		MaxIterations: 0,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.Equal(t, 0, result.IterationsUsed)
	assert.Equal(t, 0, llm.fixCallCount)
}

// TestValidateAndFix_ValidatorError propagates errors from the validator.
func TestValidateAndFix_ValidatorError(t *testing.T) {
	ctx := context.Background()

	validator := &mockValidator{
		errors: []error{errors.New("database unavailable")},
	}
	llm := &mockLLMClient{}

	_, err := generator.ValidateAndFix(ctx, "valid: yaml", generator.ValidateFixOptions{
		MaxIterations: 3,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database unavailable")
}

// TestValidateAndFix_LLMError propagates errors from the LLM Fix call.
func TestValidateAndFix_LLMError(t *testing.T) {
	ctx := context.Background()

	validator := &mockValidator{
		responses: []*generator.ValidationResult{
			{Valid: false, Errors: []generator.ValidationError{
				{Code: "ERR", Path: "x", Message: "broken"},
			}},
		},
	}
	llm := &mockLLMClient{
		fixErrors: []error{errors.New("LLM rate limited")},
	}

	_, err := generator.ValidateAndFix(ctx, "broken: yes", generator.ValidateFixOptions{
		MaxIterations: 2,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM rate limited")
}

// --- enrichErrors tests ---

// TestEnrichErrors_UnknownHandler_NilRegistry returns errors unchanged when no registry.
func TestEnrichErrors_UnknownHandler_NilRegistry(t *testing.T) {
	errs := []generator.ValidationError{
		{Code: "UNKNOWN_HANDLER", Path: "sagas[0]", Message: "unknown handler: foo.bar"},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	assert.Nil(t, enriched[0].AvailableFields)
}

// TestEnrichErrors_UnknownEventTopic_AddsSuggestion verifies that an unknown event topic
// error gets available topics added.
func TestEnrichErrors_UnknownEventTopic_AddsSuggestion(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:    "UNKNOWN_EVENT_TOPIC",
			Path:    "sagas[0].triggers[0].event_topic",
			Message: "unknown topic: position_keeping.transaction_captur.v1",
		},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	// Should have available topics populated (from topics.All())
	assert.NotEmpty(t, enriched[0].AvailableFields, "expected available topics to be populated")
}

// TestEnrichErrors_UnknownEventTopic_PreservesExistingSuggestion verifies that if a
// suggestion is already present, it is not overwritten.
func TestEnrichErrors_UnknownEventTopic_PreservesExistingSuggestion(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:       "UNKNOWN_EVENT_TOPIC",
			Path:       "sagas[0]",
			Message:    "unknown topic",
			Suggestion: "position_keeping.transaction_captured.v1",
		},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	assert.Equal(t, "position_keeping.transaction_captured.v1", enriched[0].Suggestion)
}

// TestEnrichErrors_UnknownCode_PassesThrough verifies that errors with unrecognized codes
// are returned unchanged.
func TestEnrichErrors_UnknownCode_PassesThrough(t *testing.T) {
	errs := []generator.ValidationError{
		{Code: "SOME_OTHER_CODE", Path: "foo", Message: "bar"},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	assert.Equal(t, "SOME_OTHER_CODE", enriched[0].Code)
	assert.Nil(t, enriched[0].AvailableFields)
	assert.Equal(t, "", enriched[0].Suggestion)
}

// TestEnrichErrors_EmptySlice returns an empty slice unchanged.
func TestEnrichErrors_EmptySlice(t *testing.T) {
	enriched := generator.EnrichErrors(nil, nil)
	assert.Nil(t, enriched)
}

// --- applyMutatingPhase tests ---

// TestApplyMutatingPhase_NilRegistry returns manifest unchanged.
func TestApplyMutatingPhase_NilRegistry(t *testing.T) {
	manifest := "sagas:\n  - name: test\n    script: |\n      old_handler(amount=100)"
	result := generator.ApplyMutatingPhase(manifest, nil)
	assert.Equal(t, manifest, result)
}

// TestApplyMutatingPhase_NoDeprecatedHandlers returns manifest unchanged when registry has no deprecated handlers.
func TestApplyMutatingPhase_NoDeprecatedHandlers(t *testing.T) {
	reg := generator.NewEmptySchemaRegistry()
	manifest := "sagas:\n  - name: test\n    script: |\n      print('hello')"
	result := generator.ApplyMutatingPhase(manifest, reg)
	assert.Equal(t, manifest, result)
}
