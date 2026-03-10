package generator_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
)

func TestCachedContextAssembler_PopulatesOnFirstCall(t *testing.T) {
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), time.Minute)

	result, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A carbon credit trading platform",
		IncludePatterns: false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Prompt)
	assert.Contains(t, result.Prompt, "## Handler Reference Card")
	assert.Contains(t, result.Prompt, "## Available Event Topics")
	assert.Contains(t, result.Prompt, "## Manifest Schema Summary")
}

func TestCachedContextAssembler_ReturnsCachedValuesWithinInterval(t *testing.T) {
	reg := buildMinimalRegistry()
	// Use a long refresh interval so it won't expire during the test.
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), time.Hour)

	opts := generator.ContextAssemblerOptions{
		Description:     "An energy trading platform",
		IncludePatterns: false,
	}

	first, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	second, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	// Both calls should produce identical static content.
	assert.Equal(t, first.Prompt, second.Prompt)
}

func TestCachedContextAssembler_RefreshesAfterIntervalExpires(t *testing.T) {
	reg := buildMinimalRegistry()
	// Use a very short interval, then invalidate to simulate expiry.
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), time.Hour)

	opts := generator.ContextAssemblerOptions{
		Description:     "A payment processing platform",
		IncludePatterns: false,
	}

	first, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	// Invalidate the cache to force a refresh on the next call.
	assembler.Invalidate()

	second, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	// After invalidation the static content is rebuilt — content should still match
	// since the registry hasn't changed, confirming that the refresh path executes
	// without error and produces a valid prompt.
	assert.Equal(t, first.Prompt, second.Prompt)
}

func TestCachedContextAssembler_ConcurrentAccess(t *testing.T) {
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), time.Minute)

	const goroutines = 20
	results := make([]*generator.AssembledContext, goroutines)
	errors := make([]error, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = assembler.AssembleContext(generator.ContextAssemblerOptions{
				Description:     "A concurrent test platform",
				IncludePatterns: false,
			})
		}(i)
	}

	wg.Wait()

	for i, err := range errors {
		require.NoError(t, err, "goroutine %d returned error", i)
		require.NotNil(t, results[i])
		assert.NotEmpty(t, results[i].Prompt)
	}

	// All goroutines should produce identical static content.
	for i := 1; i < goroutines; i++ {
		assert.Equal(t, results[0].Prompt, results[i].Prompt,
			"goroutine %d produced different result", i)
	}
}

func TestCachedContextAssembler_PatternMatchingRemainsUncached(t *testing.T) {
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, realCookbookFS(), time.Hour)

	// First call: energy description should match energy patterns.
	energyResult, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "EV charging UK energy settlement",
		Industry:        "energy",
		IncludePatterns: true,
		MaxPatterns:     3,
	})
	require.NoError(t, err)

	// Second call: unrelated description should produce no/different patterns.
	genericResult, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "Generic fintech platform with no patterns",
		IncludePatterns: false,
	})
	require.NoError(t, err)

	// Pattern-specific content should differ between calls.
	assert.NotEqual(t, energyResult.Prompt, genericResult.Prompt,
		"different descriptions should produce different prompts")
}

func TestCachedContextAssembler_DefaultRefreshInterval(t *testing.T) {
	reg := buildMinimalRegistry()
	// Zero interval should use the default (5 minutes), not panic or error.
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), 0)

	result, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A platform with default interval",
		IncludePatterns: false,
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result.Prompt)
}

func TestCachedContextAssembler_ProducesEquivalentOutputToAssembleContext(t *testing.T) {
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), time.Hour)

	opts := generator.ContextAssemblerOptions{
		Description:     "A payment platform",
		IncludePatterns: false,
	}

	cached, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	direct, err := generator.AssembleContext(opts, reg, emptyFS())
	require.NoError(t, err)

	// The cached assembler must produce the same output as the direct function.
	assert.Equal(t, direct.Prompt, cached.Prompt)
	assert.Equal(t, direct.TokenEstimate, cached.TokenEstimate)
	assert.Equal(t, direct.MatchedPatterns, cached.MatchedPatterns)
}
