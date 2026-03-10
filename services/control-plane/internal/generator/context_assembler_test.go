package generator_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// buildMinimalRegistry creates a registry with a single test handler for use in unit tests.
func buildMinimalRegistry() *schema.Registry {
	reg := schema.NewRegistry()
	yaml := `
service: test_service
version: "1.0"
handlers:
  test_service.do_thing:
    description: "Test handler"
    compensation_strategy: none
    params:
      amount:
        type: Decimal
        required: true
    returns:
      result:
        type: string
`
	if err := reg.LoadFromYAML([]byte(yaml)); err != nil {
		panic("buildMinimalRegistry: " + err.Error())
	}
	return reg
}

// emptyFS returns an FS with only the registry.json required by MatchPatterns.
func emptyFS() fstest.MapFS {
	return fstest.MapFS{
		"registry.json": &fstest.MapFile{Data: []byte(`{"items":[]}`)},
	}
}

func TestAssembleContext_CreateMode_AllStaticSectionsPresent(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "An energy trading platform for UK markets",
		Industry:        "energy",
		IncludePatterns: false,
	}, reg, emptyFS())

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Contains(t, result.Prompt, "## Business Description")
	assert.Contains(t, result.Prompt, "An energy trading platform for UK markets")
	assert.Contains(t, result.Prompt, "## Manifest Schema Summary")
	assert.Contains(t, result.Prompt, "## Handler Reference Card")
	assert.Contains(t, result.Prompt, "## Available Event Topics")
	assert.Contains(t, result.Prompt, "## Instructions")
}

func TestAssembleContext_CreateMode_NoCurrentEconomy(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:           "A payment platform",
		IncludePatterns:       false,
		IncludeCurrentEconomy: false,
	}, reg, emptyFS())

	require.NoError(t, err)
	assert.NotContains(t, result.Prompt, "## Current Economy")
}

func TestAssembleContext_WithIndustryHint_IncludesMatchedPatterns(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "EV charging UK energy settlement",
		Industry:        "energy",
		IncludePatterns: true,
		MaxPatterns:     3,
	}, reg, realCookbookFS())

	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Contains(t, result.Prompt, "## Relevant Patterns (copy and adapt)")
	assert.NotEmpty(t, result.MatchedPatterns)
}

func TestAssembleContext_WithoutIndustryHint_PatternsOptional(t *testing.T) {
	reg := buildMinimalRegistry()

	// With an empty FS that has no patterns, IncludePatterns should not error.
	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A fintech platform",
		IncludePatterns: true,
		MaxPatterns:     3,
	}, reg, emptyFS())

	require.NoError(t, err)
	require.NotNil(t, result)
	// No patterns matched — section should be absent.
	assert.NotContains(t, result.Prompt, "## Relevant Patterns")
	assert.Empty(t, result.MatchedPatterns)
}

func TestAssembleContext_AmendMode_IncludesCurrentManifest(t *testing.T) {
	reg := buildMinimalRegistry()

	manifest := &controlplanev1.Manifest{
		Version: "1.0.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:        "test-economy",
			Description: "Existing economy",
		},
	}

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:           "A payment platform with energy add-ons",
		IncludePatterns:       false,
		IncludeCurrentEconomy: true,
		CurrentManifest:       manifest,
	}, reg, emptyFS())

	require.NoError(t, err)
	assert.Contains(t, result.Prompt, "## Current Economy")
	assert.Contains(t, result.Prompt, "test-economy")
}

func TestAssembleContext_AmendMode_NilManifest_ReturnsError(t *testing.T) {
	reg := buildMinimalRegistry()

	// IncludeCurrentEconomy=true with nil CurrentManifest is a caller error.
	_, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:           "A payment platform",
		IncludePatterns:       false,
		IncludeCurrentEconomy: true,
		CurrentManifest:       nil,
	}, reg, emptyFS())

	require.Error(t, err)
	assert.ErrorIs(t, err, generator.ErrMissingCurrentManifest)
}

func TestAssembleContext_WithRelationshipGraph_IncludesGraphSection(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:       "An energy platform",
		IncludePatterns:   false,
		RelationshipGraph: `{"nodes":[],"edges":[]}`,
	}, reg, emptyFS())

	require.NoError(t, err)
	assert.Contains(t, result.Prompt, "## Economy Relationship Graph")
	assert.Contains(t, result.Prompt, `{"nodes":[],"edges":[]}`)
}

func TestAssembleContext_DefaultMaxPatterns(t *testing.T) {
	reg := buildMinimalRegistry()

	// MaxPatterns = 0 should default to 3.
	// MatchPatterns may prepend base patterns from extends resolution outside the cap,
	// but we verify that at most 3 patterns come from direct scoring by also running
	// with explicit MaxPatterns=3 and confirming results are equivalent.
	resultDefault, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "EV charging UK energy settlement",
		Industry:        "energy",
		IncludePatterns: true,
		MaxPatterns:     0, // should default to 3
	}, reg, realCookbookFS())
	require.NoError(t, err)

	resultExplicit, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "EV charging UK energy settlement",
		Industry:        "energy",
		IncludePatterns: true,
		MaxPatterns:     3,
	}, reg, realCookbookFS())
	require.NoError(t, err)

	assert.Equal(t, len(resultDefault.MatchedPatterns), len(resultExplicit.MatchedPatterns),
		"MaxPatterns=0 should default to 3 and produce the same result as MaxPatterns=3")
}

func TestAssembleContext_TokenEstimateReasonable(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "An energy trading platform",
		IncludePatterns: false,
	}, reg, emptyFS())

	require.NoError(t, err)

	// Token estimate should be positive.
	assert.Greater(t, result.TokenEstimate, 0)

	// Rough sanity check: estimate should be between 0.5x and 2.5x the word count.
	wordCount := len(splitWords(result.Prompt))
	assert.GreaterOrEqual(t, result.TokenEstimate, wordCount/2)
	assert.LessOrEqual(t, result.TokenEstimate, wordCount*3)
}

func TestAssembleContext_InstructionsSectionPresent(t *testing.T) {
	reg := buildMinimalRegistry()

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A carbon credit trading platform",
		IncludePatterns: false,
	}, reg, emptyFS())

	require.NoError(t, err)
	assert.Contains(t, result.Prompt, "## Instructions")
	assert.Contains(t, result.Prompt, "Generate a complete Meridian manifest YAML")
	assert.Contains(t, result.Prompt, "valid Starlark")
}

func TestAssembleContext_AmendMode_InstructionsIncludePreservation(t *testing.T) {
	reg := buildMinimalRegistry()

	manifest := &controlplanev1.Manifest{
		Version: "1.0.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name: "existing",
		},
	}

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:           "Extended energy platform",
		IncludePatterns:       false,
		IncludeCurrentEconomy: true,
		CurrentManifest:       manifest,
	}, reg, emptyFS())

	require.NoError(t, err)
	assert.Contains(t, result.Prompt, "Preserve existing")
}

func TestAssembleContext_BlankDescription_ReturnsError(t *testing.T) {
	reg := buildMinimalRegistry()

	for _, desc := range []string{"", "   ", "\t\n"} {
		_, err := generator.AssembleContext(generator.ContextAssemblerOptions{
			Description:     desc,
			IncludePatterns: false,
		}, reg, emptyFS())
		require.Error(t, err, "expected error for blank description %q", desc)
		assert.ErrorIs(t, err, generator.ErrBlankDescription)
	}
}

func TestAssembleContext_NilCookbookFS_PatternsDisabled(t *testing.T) {
	reg := buildMinimalRegistry()

	// A nil cookbookFS with IncludePatterns=true should be handled gracefully.
	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A platform",
		IncludePatterns: true,
	}, reg, nil)

	require.NoError(t, err)
	assert.Empty(t, result.MatchedPatterns)
}

func TestAssembleContext_BrokenCookbookFS_ExposesPatternMatchError(t *testing.T) {
	reg := buildMinimalRegistry()

	// An FS without registry.json will cause MatchPatterns to fail.
	brokenFS := fstest.MapFS{}

	result, err := generator.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A platform",
		IncludePatterns: true,
	}, reg, brokenFS)

	// AssembleContext should succeed (non-fatal).
	require.NoError(t, err)
	// Pattern match error is exposed on the result.
	assert.Error(t, result.PatternMatchError)
	assert.Empty(t, result.MatchedPatterns)
}

// splitWords splits a string into whitespace-delimited words, used only for test assertions.
func splitWords(s string) []string {
	return strings.Fields(s)
}
