package generator_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExtractYAML_NoFences returns full trimmed text when no code fences are present.
func TestExtractYAML_NoFences(t *testing.T) {
	input := `
instruments:
  - code: GBP
    name: British Pound
`
	result := generator.ExtractYAML(input)
	assert.Equal(t, "instruments:\n  - code: GBP\n    name: British Pound", result)
}

// TestExtractYAML_WithFences strips yaml code fences and returns inner content.
func TestExtractYAML_WithFences(t *testing.T) {
	input := "Here is your manifest:\n\n```yaml\ninstruments:\n  - code: GBP\n```\n\nAll done."
	result := generator.ExtractYAML(input)
	assert.Equal(t, "instruments:\n  - code: GBP", result)
}

// TestExtractYAML_UnclosedFence returns content after opening fence when closing fence is missing.
func TestExtractYAML_UnclosedFence(t *testing.T) {
	input := "```yaml\ninstruments:\n  - code: GBP\n"
	result := generator.ExtractYAML(input)
	assert.Equal(t, "instruments:\n  - code: GBP", result)
}

// TestExtractYAML_MultipleBlocks returns the first yaml-fenced block.
func TestExtractYAML_MultipleBlocks(t *testing.T) {
	input := "```yaml\nfirst: block\n```\n\n```yaml\nsecond: block\n```"
	result := generator.ExtractYAML(input)
	assert.Equal(t, "first: block", result)
}

// TestExtractYAML_EmptyInput returns empty string for empty input.
func TestExtractYAML_EmptyInput(t *testing.T) {
	result := generator.ExtractYAML("")
	assert.Equal(t, "", result)
}

// TestExtractYAML_OnlyWhitespace returns empty string for whitespace-only input.
func TestExtractYAML_OnlyWhitespace(t *testing.T) {
	result := generator.ExtractYAML("   \n\t  ")
	assert.Equal(t, "", result)
}

// TestBuildFixPrompt_IncludesAllErrorFields verifies the fix prompt contains
// the manifest and all structured error fields.
func TestBuildFixPrompt_IncludesAllErrorFields(t *testing.T) {
	manifest := "instruments:\n  - code: INVALID"
	errors := []generator.ValidationError{
		{
			Code:       "DUPLICATE_CODE",
			Path:       "instruments[0].code",
			Message:    "duplicate instrument code \"INVALID\"",
			Suggestion: "use a unique code",
			Available:  []string{"GBP", "USD", "EUR"},
		},
		{
			Code:    "PROTO_VALIDATION",
			Path:    "sagas[0].name",
			Message: "name is required",
		},
	}

	prompt := generator.BuildFixPrompt(manifest, errors)

	assert.Contains(t, prompt, manifest)
	assert.Contains(t, prompt, "DUPLICATE_CODE")
	assert.Contains(t, prompt, "instruments[0].code")
	assert.Contains(t, prompt, "duplicate instrument code")
	assert.Contains(t, prompt, "use a unique code")
	assert.Contains(t, prompt, "GBP, USD, EUR")
	assert.Contains(t, prompt, "PROTO_VALIDATION")
	assert.Contains(t, prompt, "sagas[0].name")
	assert.Contains(t, prompt, "name is required")
}

// TestBuildFixPrompt_NoSuggestionOrAvailable verifies errors without optional
// fields render correctly without extra lines.
func TestBuildFixPrompt_NoSuggestionOrAvailable(t *testing.T) {
	errors := []generator.ValidationError{
		{
			Code:    "CEL_EXPRESSION_TOO_LONG",
			Path:    "mappings[0].expression",
			Message: "expression exceeds 4096 bytes",
		},
	}

	prompt := generator.BuildFixPrompt("key: val", errors)

	assert.Contains(t, prompt, "CEL_EXPRESSION_TOO_LONG")
	assert.NotContains(t, prompt, "Suggestion:")
	assert.NotContains(t, prompt, "Available values:")
}

// TestBuildFixPrompt_ManifestWithoutTrailingNewline ensures a newline is appended
// before the closing fence so the YAML block is well-formed.
func TestBuildFixPrompt_ManifestWithoutTrailingNewline(t *testing.T) {
	prompt := generator.BuildFixPrompt("key: val", []generator.ValidationError{
		{Code: "ERR", Path: "key", Message: "bad value"},
	})

	// The manifest block should be properly closed
	assert.Contains(t, prompt, "key: val\n```")
}

// TestNewClaudeLLMClient_DefaultModel verifies that an empty model string
// falls back to DefaultModel.
func TestNewClaudeLLMClient_DefaultModel(t *testing.T) {
	client := generator.NewClaudeLLMClient("test-key", "")
	require.NotNil(t, client)
	assert.Equal(t, generator.DefaultModel, client.Model())
}

// TestNewClaudeLLMClient_CustomModel verifies that a provided model string is used.
func TestNewClaudeLLMClient_CustomModel(t *testing.T) {
	const customModel = "claude-opus-4-5"
	client := generator.NewClaudeLLMClient("test-key", customModel)
	require.NotNil(t, client)
	assert.Equal(t, customModel, client.Model())
}
