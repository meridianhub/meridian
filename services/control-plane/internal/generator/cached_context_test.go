package generator_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// countingBuildStatics returns a buildStatics function that increments a counter each
// time it is called. The counter lets tests verify cache hit vs. cache miss behavior.
func countingBuildStatics(counter *atomic.Int64) func(*schema.Registry) generator.StaticComponents {
	return func(_ *schema.Registry) generator.StaticComponents {
		counter.Add(1)
		return generator.StaticComponents{} // content is irrelevant for cache behavior tests
	}
}

func newCountingAssembler(t *testing.T, interval time.Duration) (*generator.CachedContextAssembler, *atomic.Int64) {
	t.Helper()
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, emptyFS(), interval)
	var counter atomic.Int64
	assembler.SetBuildStatics(countingBuildStatics(&counter))
	return assembler, &counter
}

func TestCachedContextAssembler_PopulatesOnFirstCall(t *testing.T) {
	assembler, counter := newCountingAssembler(t, time.Hour)

	_, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A carbon credit trading platform",
		IncludePatterns: false,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1), counter.Load(), "buildStatics should be called exactly once on first call")
}

func TestCachedContextAssembler_ReturnsCachedValuesWithinInterval(t *testing.T) {
	assembler, counter := newCountingAssembler(t, time.Hour)

	opts := generator.ContextAssemblerOptions{
		Description:     "An energy trading platform",
		IncludePatterns: false,
	}

	_, err := assembler.AssembleContext(opts)
	require.NoError(t, err)

	_, err = assembler.AssembleContext(opts)
	require.NoError(t, err)

	_, err = assembler.AssembleContext(opts)
	require.NoError(t, err)

	assert.Equal(t, int64(1), counter.Load(), "buildStatics should only be called once within the refresh interval")
}

func TestCachedContextAssembler_RefreshesAfterIntervalExpires(t *testing.T) {
	assembler, counter := newCountingAssembler(t, time.Hour)

	opts := generator.ContextAssemblerOptions{
		Description:     "A payment processing platform",
		IncludePatterns: false,
	}

	_, err := assembler.AssembleContext(opts)
	require.NoError(t, err)
	assert.Equal(t, int64(1), counter.Load())

	// Force cache expiry.
	assembler.Invalidate()

	_, err = assembler.AssembleContext(opts)
	require.NoError(t, err)

	assert.Equal(t, int64(2), counter.Load(), "buildStatics should be called again after Invalidate")
}

func TestCachedContextAssembler_ConcurrentAccess(t *testing.T) {
	assembler, counter := newCountingAssembler(t, time.Hour)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = assembler.AssembleContext(generator.ContextAssemblerOptions{
				Description:     "A concurrent test platform",
				IncludePatterns: false,
			})
		}()
	}

	wg.Wait()

	// With double-checked locking, at most a small number of concurrent goroutines
	// may race past the read lock before the write lock is taken. In practice with
	// a 1-hour interval only one refresh should occur, but allow a small buffer for
	// concurrent first-call races.
	assert.LessOrEqual(t, counter.Load(), int64(5),
		"buildStatics should be called very few times under concurrent access (expected ~1)")
	assert.GreaterOrEqual(t, counter.Load(), int64(1), "buildStatics must be called at least once")
}

func TestCachedContextAssembler_PatternMatchingRemainsUncached(t *testing.T) {
	// Use a real assembler with actual buildStatics so pattern matching runs.
	reg := buildMinimalRegistry()
	assembler := generator.NewCachedContextAssembler(reg, realCookbookFS(), time.Hour)

	// Both calls use IncludePatterns: true so pattern matching runs in each.
	// The difference is the description/industry hint — energy vs. generic.
	// If MatchedPatterns were being cached and reused, the second call would
	// incorrectly return energy patterns for a generic description.
	energyResult, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "EV charging UK energy settlement",
		Industry:        "energy",
		IncludePatterns: true,
		MaxPatterns:     3,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, energyResult.MatchedPatterns, "energy description should match patterns")
	assert.Contains(t, energyResult.Prompt, "## Relevant Patterns")

	genericResult, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A generic fintech platform with no industry patterns",
		IncludePatterns: true,
		MaxPatterns:     3,
	})
	require.NoError(t, err)

	// Pattern sets must differ: energy patterns should not appear for a generic description.
	assert.NotEqual(t, energyResult.MatchedPatterns, genericResult.MatchedPatterns,
		"MatchedPatterns must be computed per-request, not reused from cache")
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

func TestCachedContextAssembler_NilRegistry_ReturnsError(t *testing.T) {
	assembler := generator.NewCachedContextAssembler(nil, emptyFS(), time.Hour)

	_, err := assembler.AssembleContext(generator.ContextAssemblerOptions{
		Description:     "A platform",
		IncludePatterns: false,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, generator.ErrMissingRegistry)
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
