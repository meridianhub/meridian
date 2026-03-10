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

// --- ValidateAndFix input validation tests ---

// TestValidateAndFix_NilValidator returns an error immediately.
func TestValidateAndFix_NilValidator(t *testing.T) {
	_, err := generator.ValidateAndFix(context.Background(), "x: 1", generator.ValidateFixOptions{
		MaxIterations: 0,
		Validator:     nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validator is required")
}

// TestValidateAndFix_NegativeMaxIterations returns an error immediately.
func TestValidateAndFix_NegativeMaxIterations(t *testing.T) {
	_, err := generator.ValidateAndFix(context.Background(), "x: 1", generator.ValidateFixOptions{
		MaxIterations: -1,
		Validator:     &mockValidator{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaxIterations must be >= 0")
}

// TestValidateAndFix_NilLLMClientWithIterations returns an error when fixes are expected.
func TestValidateAndFix_NilLLMClientWithIterations(t *testing.T) {
	_, err := generator.ValidateAndFix(context.Background(), "x: 1", generator.ValidateFixOptions{
		MaxIterations: 2,
		Validator:     &mockValidator{},
		LLMClient:     nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM client is required")
}

// --- replaceDeprecatedHandler (text manipulation) tests ---

// TestReplaceDeprecatedHandler_BasicRename verifies a simple handler name replacement.
func TestReplaceDeprecatedHandler_BasicRename(t *testing.T) {
	script := "old_handler(amount=100, direction='CREDIT')"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Equal(t, "new_handler(amount=100, direction='CREDIT')", result)
}

// TestReplaceDeprecatedHandler_SkipsStringLiteral verifies that handler names inside
// string literals are not replaced.
func TestReplaceDeprecatedHandler_SkipsStringLiteral(t *testing.T) {
	script := `x = "old_handler(amount=1)"` + "\nold_handler(amount=2)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	// The string literal occurrence must be preserved; only the real call is replaced.
	assert.Contains(t, result, `"old_handler(amount=1)"`)
	assert.Contains(t, result, "new_handler(amount=2)")
}

// TestReplaceDeprecatedHandler_SkipsLineComment verifies that handler names in comments
// are not replaced.
func TestReplaceDeprecatedHandler_SkipsLineComment(t *testing.T) {
	script := "# old_handler(amount=1)\nold_handler(amount=2)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Contains(t, result, "# old_handler(amount=1)")
	assert.Contains(t, result, "new_handler(amount=2)")
}

// TestReplaceDeprecatedHandler_WordBoundary verifies that "old_handler_extended" is not
// matched when looking for "old_handler".
func TestReplaceDeprecatedHandler_WordBoundary(t *testing.T) {
	script := "old_handler_extended(amount=1)\nold_handler(amount=2)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Contains(t, result, "old_handler_extended(amount=1)")
	assert.Contains(t, result, "new_handler(amount=2)")
}

// TestReplaceDeprecatedHandler_NoOccurrence returns script unchanged when name absent.
func TestReplaceDeprecatedHandler_NoOccurrence(t *testing.T) {
	script := "different_handler(amount=1)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Equal(t, script, result)
}

// --- findHandlerCall tests ---

// TestFindHandlerCall_BasicMatch finds a handler call in plain text.
func TestFindHandlerCall_BasicMatch(t *testing.T) {
	s := "old_handler(x=1)"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, 0, idx)
}

// TestFindHandlerCall_InsideString returns -1 for occurrences inside string literals.
func TestFindHandlerCall_InsideString(t *testing.T) {
	s := `msg = "old_handler(x=1)"`
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

// TestFindHandlerCall_InsideComment returns -1 for occurrences in line comments.
func TestFindHandlerCall_InsideComment(t *testing.T) {
	s := "# old_handler(x=1)"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

// --- extractHandlerName tests ---

// TestExtractHandlerName_WithHashParam strips the #param suffix before extraction.
func TestExtractHandlerName_WithHashParam(t *testing.T) {
	path := "sagas[0]:position_keeping.initiate_log#amount"
	name := generator.ExtractHandlerName(path)
	assert.Equal(t, "position_keeping.initiate_log", name)
}

// TestExtractHandlerName_WithoutHash extracts handler from colon-delimited path.
func TestExtractHandlerName_WithoutHash(t *testing.T) {
	path := "sagas[0]:position_keeping.initiate_log"
	name := generator.ExtractHandlerName(path)
	assert.Equal(t, "position_keeping.initiate_log", name)
}

// TestExtractHandlerName_Empty returns empty for unrecognized path.
func TestExtractHandlerName_Empty(t *testing.T) {
	name := generator.ExtractHandlerName("sagas[0].name")
	assert.Equal(t, "", name)
}
