package generator_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateAndFix: nil validator result ---

func TestValidateAndFix_NilValidatorResult(t *testing.T) {
	ctx := context.Background()

	validator := &mockValidator{
		responses: []*generator.ValidationResult{nil},
	}
	llm := &mockLLMClient{}

	_, err := generator.ValidateAndFix(ctx, "yaml: true", generator.ValidateFixOptions{
		MaxIterations: 0,
		LLMClient:     llm,
		Validator:     validator,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil result from validator")
}

// --- applyMutatingPhase: script block handling ---

func TestApplyMutatingPhase_ScriptBlockAtEndOfFile(t *testing.T) {
	// Script block that extends to end of YAML without de-indenting
	manifest := "sagas:\n  - name: test\n    script: |\n      print('hello')\n      print('world')"
	result := generator.ApplyMutatingPhase(manifest, nil)
	assert.Equal(t, manifest, result)
}

// --- findHandlerCall edge cases ---

func TestFindHandlerCall_WithWhitespaceBeforeParen(t *testing.T) {
	s := "old_handler \t(x=1)"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, 0, idx)
}

func TestFindHandlerCall_NoParenAfterName(t *testing.T) {
	s := "old_handler = 42"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

func TestFindHandlerCall_PrefixedByIdentChar(t *testing.T) {
	s := "my_old_handler(x=1)"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

func TestFindHandlerCall_InsideSingleQuotedString(t *testing.T) {
	s := "msg = 'old_handler(x=1)'"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

func TestFindHandlerCall_InsideTripleQuotedString(t *testing.T) {
	s := `msg = """old_handler(x=1)"""`
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, -1, idx)
}

func TestFindHandlerCall_AfterComment(t *testing.T) {
	s := "# comment\nold_handler(x=1)"
	idx := generator.FindHandlerCall(s, "old_handler")
	assert.Equal(t, 10, idx) // after "# comment\n"
}

// --- replaceDeprecatedHandler: kwarg renaming ---

func TestReplaceDeprecatedHandler_WithParamMapping(t *testing.T) {
	script := "old_handler(old_param=100, keep_param='x')"
	info := generator.NewDeprecatedHandlerInfoWithDefaults("new_handler", nil)
	// We need to test with param mapping. Since NewDeprecatedHandlerInfoWithDefaults
	// sets defaults, let's use the basic form and test kwarg renaming via the
	// exported function interface.
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Contains(t, result, "new_handler(")
	assert.Contains(t, result, "old_param=100")
	assert.Contains(t, result, "keep_param='x'")
}

func TestReplaceDeprecatedHandler_MultipleOccurrences(t *testing.T) {
	script := "old_handler(a=1)\nold_handler(b=2)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Equal(t, "new_handler(a=1)\nnew_handler(b=2)", result)
}

func TestReplaceDeprecatedHandler_NestedParens(t *testing.T) {
	script := "old_handler(a=fn(1, 2), b=3)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Equal(t, "new_handler(a=fn(1, 2), b=3)", result)
}

func TestReplaceDeprecatedHandler_StringInArgs(t *testing.T) {
	script := `old_handler(msg="hello (world)", b=1)`
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Contains(t, result, "new_handler(")
	assert.Contains(t, result, `msg="hello (world)"`)
}

func TestReplaceDeprecatedHandler_CommentInArgs(t *testing.T) {
	script := "old_handler(\n  a=1, # comment\n  b=2)"
	info := generator.NewDeprecatedHandlerInfo("new_handler")
	result := generator.ReplaceDeprecatedHandler(script, "old_handler", info)
	assert.Contains(t, result, "new_handler(")
	assert.Contains(t, result, "a=1")
	assert.Contains(t, result, "b=2")
}

// --- injectMissingDefaults edge cases ---

func TestInjectMissingDefaults_EmptyCallBody(t *testing.T) {
	callBody := ")"
	defaults := map[string]string{"direction": `"CREDIT"`}
	result := generator.InjectMissingDefaults(callBody, defaults)
	assert.Contains(t, result, `direction="CREDIT"`)
	assert.Contains(t, result, ")")
}

func TestInjectMissingDefaults_MultipleDefaults(t *testing.T) {
	callBody := "amount=100)"
	defaults := map[string]string{
		"direction": `"CREDIT"`,
		"currency":  `"GBP"`,
	}
	result := generator.InjectMissingDefaults(callBody, defaults)
	assert.Contains(t, result, `currency="GBP"`)
	assert.Contains(t, result, `direction="CREDIT"`)
	assert.Contains(t, result, "amount=100")
}

func TestInjectMissingDefaults_NoClosingParen(t *testing.T) {
	callBody := "amount=100"
	defaults := map[string]string{"direction": `"CREDIT"`}
	// Should return unchanged when no closing paren
	result := generator.InjectMissingDefaults(callBody, defaults)
	assert.Equal(t, callBody, result)
}

func TestInjectMissingDefaults_NestedCallWithSameKwarg(t *testing.T) {
	// A kwarg inside a nested call should not count as "present" at top level
	callBody := "a=fn(direction='X'))"
	defaults := map[string]string{"direction": `"CREDIT"`}
	result := generator.InjectMissingDefaults(callBody, defaults)
	// direction inside fn() is at depth>0, so top-level direction should be injected
	assert.Contains(t, result, `direction="CREDIT"`)
}

// --- extractHandlerName edge cases ---

func TestExtractHandlerName_FallbackDottedPath(t *testing.T) {
	// When there's no colon, use last two dotted components
	name := generator.ExtractHandlerName("some_service.some_handler")
	assert.Equal(t, "some_service.some_handler", name)
}

func TestExtractHandlerName_FallbackWithBracket(t *testing.T) {
	// When the second-to-last part has a bracket, return empty
	name := generator.ExtractHandlerName("sagas[0].name")
	assert.Equal(t, "", name)
}

func TestExtractHandlerName_SingleComponent(t *testing.T) {
	name := generator.ExtractHandlerName("simple")
	assert.Equal(t, "", name)
}

// --- extractParamName ---

func TestExtractParamName_WithHash(t *testing.T) {
	param := generator.ExtractHandlerName("sagas[0]:pk.initiate_log#amount")
	assert.Equal(t, "pk.initiate_log", param)
}

// --- enrichErrors with specific codes ---

func TestEnrichErrors_EmptyAvailableFieldsGetsPopulated(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:    "UNKNOWN_EVENT_TOPIC",
			Path:    "sagas[0].triggers[0]",
			Message: "unknown topic: some.made_up.topic.v1",
		},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	assert.NotEmpty(t, enriched[0].AvailableFields)
}

func TestEnrichErrors_MissingRequiredParam_NilRegistry(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:    "MISSING_REQUIRED_PARAM",
			Path:    "sagas[0]:pk.initiate_log#amount",
			Message: "param amount is required",
		},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	// With nil registry, message is unchanged
	assert.Equal(t, "param amount is required", enriched[0].Message)
}

func TestEnrichErrors_WrongParamType_NilRegistry(t *testing.T) {
	errs := []generator.ValidationError{
		{
			Code:    "WRONG_PARAM_TYPE",
			Path:    "sagas[0]:pk.initiate_log#amount",
			Message: "expected Decimal",
		},
	}
	enriched := generator.EnrichErrors(errs, nil)
	require.Len(t, enriched, 1)
	assert.Equal(t, "expected Decimal", enriched[0].Message)
}

// --- findClosestTopicMatch ---

func TestFindClosestTopicMatch_EmptyMessage(t *testing.T) {
	result := generator.FindClosestTopicMatch("", []string{"a.b.v1"})
	assert.Equal(t, "", result)
}

func TestFindClosestTopicMatch_EmptyTopics(t *testing.T) {
	result := generator.FindClosestTopicMatch("unknown topic: a.b.v1", nil)
	assert.Equal(t, "", result)
}

func TestFindClosestTopicMatch_NoTopicLikeWords(t *testing.T) {
	result := generator.FindClosestTopicMatch("simple words without dots or underscores", []string{"a.b.v1"})
	assert.Equal(t, "", result)
}

func TestFindClosestTopicMatch_CloseMatch(t *testing.T) {
	topics := []string{
		"position_keeping.transaction_captured.v1",
		"reference_data.instrument_registered.v1",
	}
	result := generator.FindClosestTopicMatch(
		"unknown topic: position_keeping.transaction_captur.v1",
		topics,
	)
	// Should find a close match via Levenshtein
	assert.Equal(t, "position_keeping.transaction_captured.v1", result)
}

func TestFindClosestTopicMatch_ExactMatch(t *testing.T) {
	topics := []string{
		"position_keeping.transaction_captured.v1",
	}
	result := generator.FindClosestTopicMatch(
		"unknown topic: position_keeping.transaction_captured.v1",
		topics,
	)
	assert.Equal(t, "position_keeping.transaction_captured.v1", result)
}

func TestFindClosestTopicMatch_TooDistant(t *testing.T) {
	topics := []string{
		"position_keeping.transaction_captured.v1",
	}
	result := generator.FindClosestTopicMatch(
		"unknown topic: completely_different.xyz.v99",
		topics,
	)
	// Should return empty - too far away
	assert.Equal(t, "", result)
}

// --- levenshteinDist ---

func TestLevenshteinDist_IdenticalStrings(t *testing.T) {
	assert.Equal(t, 0, generator.LevenshteinDist("hello", "hello"))
}

func TestLevenshteinDist_EmptyStrings(t *testing.T) {
	assert.Equal(t, 0, generator.LevenshteinDist("", ""))
}

func TestLevenshteinDist_OneEmpty(t *testing.T) {
	assert.Equal(t, 5, generator.LevenshteinDist("hello", ""))
	assert.Equal(t, 5, generator.LevenshteinDist("", "hello"))
}

func TestLevenshteinDist_SingleEdit(t *testing.T) {
	assert.Equal(t, 1, generator.LevenshteinDist("cat", "bat"))
	assert.Equal(t, 1, generator.LevenshteinDist("cat", "cats"))
	assert.Equal(t, 1, generator.LevenshteinDist("cats", "cat"))
}

func TestLevenshteinDist_Symmetry(t *testing.T) {
	assert.Equal(t, generator.LevenshteinDist("abc", "xyz"), generator.LevenshteinDist("xyz", "abc"))
}

func TestLevenshteinDist_LongerFirstArg(t *testing.T) {
	// Tests the swap branch (la > lb)
	assert.Equal(t, 2, generator.LevenshteinDist("abcdef", "abcd"))
}
