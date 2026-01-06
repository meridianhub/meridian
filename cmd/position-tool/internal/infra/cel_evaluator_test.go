package infra

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	celcore "github.com/meridianhub/meridian/services/reference-data/cel"
)

func TestNewCELEvaluator(t *testing.T) {
	t.Run("creates evaluator with valid compiler", func(t *testing.T) {
		compiler, err := celcore.NewCompiler()
		require.NoError(t, err)

		eval, err := NewCELEvaluator(compiler)
		require.NoError(t, err)
		assert.NotNil(t, eval)
	})

	t.Run("returns error for nil compiler", func(t *testing.T) {
		eval, err := NewCELEvaluator(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilCompiler)
		assert.Nil(t, eval)
	})
}

func TestNewCELEvaluatorDefault(t *testing.T) {
	eval, err := NewCELEvaluatorDefault()
	require.NoError(t, err)
	assert.NotNil(t, eval)
}

func TestCELEvaluator_EvaluateBucketKey(t *testing.T) {
	eval, err := NewCELEvaluatorDefault()
	require.NoError(t, err)

	t.Run("evaluates simple bucket key expression", func(t *testing.T) {
		attrs := map[string]string{
			"region": "us-east-1",
			"type":   "carbon",
		}

		key, err := eval.EvaluateBucketKey(
			`bucket_key([attributes["region"], attributes["type"]])`,
			attrs,
		)
		require.NoError(t, err)
		assert.Len(t, key, 64) // SHA256 = 32 bytes = 64 hex chars
	})

	t.Run("returns same key for same input (deterministic)", func(t *testing.T) {
		attrs := map[string]string{
			"a": "foo",
			"b": "bar",
		}
		expr := `bucket_key([attributes["a"], attributes["b"]])`

		key1, err := eval.EvaluateBucketKey(expr, attrs)
		require.NoError(t, err)

		key2, err := eval.EvaluateBucketKey(expr, attrs)
		require.NoError(t, err)

		assert.Equal(t, key1, key2, "same input should produce same hash")
	})

	t.Run("returns different key for different input", func(t *testing.T) {
		expr := `bucket_key([attributes["x"]])`

		key1, err := eval.EvaluateBucketKey(expr, map[string]string{"x": "foo"})
		require.NoError(t, err)

		key2, err := eval.EvaluateBucketKey(expr, map[string]string{"x": "bar"})
		require.NoError(t, err)

		assert.NotEqual(t, key1, key2, "different input should produce different hash")
	})

	t.Run("returns error for empty expression", func(t *testing.T) {
		key, err := eval.EvaluateBucketKey("", map[string]string{})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyExpression)
		assert.Empty(t, key)
	})

	t.Run("returns error for invalid expression", func(t *testing.T) {
		key, err := eval.EvaluateBucketKey("invalid_func()", map[string]string{})
		require.Error(t, err)
		assert.Empty(t, key)
	})

	t.Run("returns error for missing attribute", func(t *testing.T) {
		// CEL map access throws error for missing keys
		key, err := eval.EvaluateBucketKey(
			`bucket_key([attributes["missing"]])`,
			map[string]string{},
		)
		// CEL returns "no such key" error for missing map keys
		require.Error(t, err)
		assert.Empty(t, key)
	})
}

func TestCELEvaluator_CachingBehavior(t *testing.T) {
	eval, err := NewCELEvaluatorDefault()
	require.NoError(t, err)

	t.Run("caches compiled expressions", func(t *testing.T) {
		expr := `bucket_key([attributes["cached"]])`
		attrs := map[string]string{"cached": "value"}

		assert.Equal(t, 0, eval.CachedExpressionCount())

		_, err := eval.EvaluateBucketKey(expr, attrs)
		require.NoError(t, err)
		assert.Equal(t, 1, eval.CachedExpressionCount())

		// Second call should use cached program
		_, err = eval.EvaluateBucketKey(expr, attrs)
		require.NoError(t, err)
		assert.Equal(t, 1, eval.CachedExpressionCount())

		// Different expression should add to cache
		_, err = eval.EvaluateBucketKey(
			`bucket_key([attributes["other"]])`,
			map[string]string{"other": "value"},
		)
		require.NoError(t, err)
		assert.Equal(t, 2, eval.CachedExpressionCount())
	})

	t.Run("clear cache works", func(t *testing.T) {
		eval2, err := NewCELEvaluatorDefault()
		require.NoError(t, err)

		_, err = eval2.EvaluateBucketKey(`bucket_key([attributes["x"]])`, map[string]string{"x": "y"})
		require.NoError(t, err)
		assert.Equal(t, 1, eval2.CachedExpressionCount())

		eval2.ClearCache()
		assert.Equal(t, 0, eval2.CachedExpressionCount())
	})
}

func TestCELEvaluator_ConcurrentAccess(t *testing.T) {
	eval, err := NewCELEvaluatorDefault()
	require.NoError(t, err)

	expr := `bucket_key([attributes["concurrent"]])`
	numGoroutines := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	results := make([]string, numGoroutines)
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			key, err := eval.EvaluateBucketKey(expr, map[string]string{"concurrent": "value"})
			results[idx] = key
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errors {
		require.NoError(t, err, "goroutine %d should not error", i)
	}

	// All should produce the same result
	for i, key := range results {
		assert.Equal(t, results[0], key, "goroutine %d should produce same result", i)
	}

	// Should have only cached one expression
	assert.Equal(t, 1, eval.CachedExpressionCount())
}

func BenchmarkCELEvaluator_EvaluateBucketKey(b *testing.B) {
	eval, err := NewCELEvaluatorDefault()
	require.NoError(b, err)

	expr := `bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`
	attrs := map[string]string{
		"type":    "carbon",
		"region":  "eu-west",
		"vintage": "2024",
	}

	// Pre-warm the cache
	_, _ = eval.EvaluateBucketKey(expr, attrs)

	b.ResetTimer()
	for b.Loop() {
		_, err := eval.EvaluateBucketKey(expr, attrs)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCELEvaluator_EvaluateBucketKey_NoCache(b *testing.B) {
	expr := `bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`
	attrs := map[string]string{
		"type":    "carbon",
		"region":  "eu-west",
		"vintage": "2024",
	}

	b.ResetTimer()
	for b.Loop() {
		eval, err := NewCELEvaluatorDefault()
		if err != nil {
			b.Fatal(err)
		}
		_, err = eval.EvaluateBucketKey(expr, attrs)
		if err != nil {
			b.Fatal(err)
		}
	}
}
