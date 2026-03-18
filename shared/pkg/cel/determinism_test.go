package cel

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBucketKeyDeterminism10kConcurrent verifies that bucket_key produces identical
// results across 10,000 concurrent evaluations across multiple goroutines.
// This tests that map key sorting is deterministic regardless of execution order.
//
// This is CRITICAL for data integrity: bucket keys are used to group fungible
// quantities, and any variation would corrupt position calculations.
func TestBucketKeyDeterminism10kConcurrent(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["grade"]])`)
	require.NoError(t, err)

	// Fixed input - every evaluation should produce the same hash
	input := map[string]any{
		"attributes": map[string]string{
			"region": "US",
			"grade":  "A",
		},
	}

	// Get the expected result first
	expected, _, err := prg.Eval(input)
	require.NoError(t, err)
	expectedHash := expected.Value().(string)
	t.Logf("Expected bucket_key hash: %s", expectedHash)

	const iterations = 10000
	numWorkers := runtime.GOMAXPROCS(0) * 2 // 2x CPU cores for good concurrency pressure
	iterationsPerWorker := iterations / numWorkers

	var wg sync.WaitGroup
	failures := make(chan string, iterations) // Buffer for failure messages

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterationsPerWorker; i++ {
				result, _, evalErr := prg.Eval(input)
				if evalErr != nil {
					failures <- "evaluation error: " + evalErr.Error()
					continue
				}
				hash := result.Value().(string)
				if hash != expectedHash {
					failures <- "hash mismatch at iteration " + string(rune(i))
				}
			}
		}()
	}

	wg.Wait()
	close(failures)

	// Collect all failures
	failureMessages := make([]string, 0, iterations)
	for msg := range failures {
		failureMessages = append(failureMessages, msg)
	}

	assert.Empty(t, failureMessages, "All %d iterations should produce identical hash", iterations)
	t.Logf("Successfully verified determinism across %d concurrent iterations with %d workers", iterations, numWorkers)
}

// TestBucketKeyDeterminismWithMultipleAttributes verifies determinism with
// different numbers of attributes in the bucket_key expression.
func TestBucketKeyDeterminismWithMultipleAttributes(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		attributes map[string]string
	}{
		{
			name:       "single attribute",
			expression: `bucket_key([attributes["region"]])`,
			attributes: map[string]string{"region": "EU"},
		},
		{
			name:       "two attributes",
			expression: `bucket_key([attributes["type"], attributes["vintage"]])`,
			attributes: map[string]string{"type": "carbon", "vintage": "2024"},
		},
		{
			name:       "three attributes",
			expression: `bucket_key([attributes["region"], attributes["grade"], attributes["source"]])`,
			attributes: map[string]string{"region": "APAC", "grade": "B", "source": "solar"},
		},
		{
			name:       "five attributes",
			expression: `bucket_key([attributes["a"], attributes["b"], attributes["c"], attributes["d"], attributes["e"]])`,
			attributes: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileBucketKey(tt.expression)
			require.NoError(t, err)

			input := map[string]any{"attributes": tt.attributes}

			// Get expected hash
			expected, _, err := prg.Eval(input)
			require.NoError(t, err)
			expectedHash := expected.Value().(string)

			// Run 1000 iterations concurrently
			const iterations = 1000
			var wg sync.WaitGroup
			var failCount atomic.Int32

			for i := 0; i < iterations; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, _, evalErr := prg.Eval(input)
					if evalErr != nil || result.Value().(string) != expectedHash {
						failCount.Add(1)
					}
				}()
			}

			wg.Wait()
			assert.Equal(t, int32(0), failCount.Load(), "All iterations should match expected hash")
		})
	}
}

// TestBucketKeyDeterminismMapKeyOrdering verifies that bucket_key produces
// consistent results regardless of map iteration order in the Go runtime.
// Go maps intentionally randomize iteration order, which could affect
// expressions that access multiple map keys.
func TestBucketKeyDeterminismMapKeyOrdering(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Expression that accesses multiple attributes - order matters for hash
	prg, err := c.CompileBucketKey(`bucket_key([attributes["z"], attributes["m"], attributes["a"]])`)
	require.NoError(t, err)

	// Large map with many keys to increase randomization effects
	attrs := map[string]string{
		"a": "alpha",
		"b": "beta",
		"c": "gamma",
		"d": "delta",
		"e": "epsilon",
		"f": "zeta",
		"g": "eta",
		"h": "theta",
		"i": "iota",
		"j": "kappa",
		"k": "lambda",
		"l": "mu",
		"m": "nu",
		"n": "xi",
		"o": "omicron",
		"p": "pi",
		"q": "rho",
		"r": "sigma",
		"s": "tau",
		"t": "upsilon",
		"u": "phi",
		"v": "chi",
		"w": "psi",
		"x": "omega",
		"y": "final",
		"z": "end",
	}

	input := map[string]any{"attributes": attrs}

	// Get expected result
	expected, _, err := prg.Eval(input)
	require.NoError(t, err)
	expectedHash := expected.Value().(string)

	// Run many iterations to exercise map randomization
	const iterations = 5000
	for i := 0; i < iterations; i++ {
		result, _, evalErr := prg.Eval(input)
		require.NoError(t, evalErr, "iteration %d", i)
		require.Equal(t, expectedHash, result.Value().(string), "iteration %d", i)
	}
}

// TestBucketKeyDeterminismUnderLoad verifies determinism under CPU pressure.
// This catches race conditions that only manifest under load.
func TestBucketKeyDeterminismUnderLoad(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["grade"]])`)
	require.NoError(t, err)

	input := map[string]any{
		"attributes": map[string]string{
			"region": "US-EAST",
			"grade":  "AA",
		},
	}

	expected, _, err := prg.Eval(input)
	require.NoError(t, err)
	expectedHash := expected.Value().(string)

	// Create CPU pressure with background work
	ctx := make(chan struct{})
	var backgroundWg sync.WaitGroup
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		backgroundWg.Add(1)
		go func() {
			defer backgroundWg.Done()
			x := 0
			for {
				select {
				case <-ctx:
					return
				default:
					x = (x + 1) % 1000000 // CPU burn
				}
			}
		}()
	}

	// Run determinism test under load
	const iterations = 5000
	var testWg sync.WaitGroup
	var failCount atomic.Int32

	for i := 0; i < iterations; i++ {
		testWg.Add(1)
		go func() {
			defer testWg.Done()
			result, _, evalErr := prg.Eval(input)
			if evalErr != nil || result.Value().(string) != expectedHash {
				failCount.Add(1)
			}
		}()
	}

	testWg.Wait()
	close(ctx)
	backgroundWg.Wait()

	assert.Equal(t, int32(0), failCount.Load(), "All iterations should match under CPU load")
}

// TestBucketKeyDeterminismTimeSensitivity verifies that bucket_key results
// are not affected by timing or system state changes.
func TestBucketKeyDeterminismTimeSensitivity(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["type"], attributes["region"]])`)
	require.NoError(t, err)

	input := map[string]any{
		"attributes": map[string]string{
			"type":   "energy",
			"region": "EMEA",
		},
	}

	// Get baseline
	baseline, _, err := prg.Eval(input)
	require.NoError(t, err)
	baselineHash := baseline.Value().(string)

	// Evaluate at different time intervals
	intervals := []time.Duration{
		1 * time.Millisecond,
		10 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
	}

	for _, interval := range intervals {
		//nolint:forbidigo // advances time between evaluations to verify CEL determinism across temporal gaps
		time.Sleep(interval)
		result, _, err := prg.Eval(input)
		require.NoError(t, err)
		assert.Equal(t, baselineHash, result.Value().(string),
			"Hash should be identical after %v delay", interval)
	}
}
