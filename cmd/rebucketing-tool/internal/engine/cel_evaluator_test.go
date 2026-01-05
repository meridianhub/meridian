package engine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCELEvaluator(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)
	require.NotNil(t, evaluator)
	assert.NotNil(t, evaluator.compiler)
	assert.NotNil(t, evaluator.programs)
}

func TestCELEvaluator_CompileBucketKeyExpression(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name        string
		expression  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid simple expression",
			expression: `bucket_key([attributes["region"], attributes["category"]])`,
			wantErr:    false,
		},
		{
			name:       "valid expression with has check",
			expression: `has(attributes.region) ? bucket_key([attributes.region]) : "default"`,
			wantErr:    false,
		},
		{
			name:       "valid constant expression",
			expression: `"static-bucket"`,
			wantErr:    false,
		},
		{
			name:        "empty expression",
			expression:  "",
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name:        "invalid syntax",
			expression:  `bucket_key([attributes["region"`,
			wantErr:     true,
			errContains: "invalid CEL expression",
		},
		{
			name:        "undefined function",
			expression:  `unknown_function()`,
			wantErr:     true,
			errContains: "invalid CEL expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			program, err := evaluator.CompileBucketKeyExpression(tt.expression)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, program)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, program)
			}
		})
	}
}

func TestCELEvaluator_CompileBucketKeyExpression_Caching(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	expression := `bucket_key([attributes["region"]])`

	// First compilation
	program1, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)
	require.NotNil(t, program1)

	// Cache should have 1 entry
	assert.Equal(t, 1, evaluator.CacheSize())

	// Second compilation should return cached program
	program2, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)
	require.NotNil(t, program2)

	// Cache should still have 1 entry
	assert.Equal(t, 1, evaluator.CacheSize())

	// Different expression should add to cache
	_, err = evaluator.CompileBucketKeyExpression(`"different"`)
	require.NoError(t, err)
	assert.Equal(t, 2, evaluator.CacheSize())

	// Clear cache
	evaluator.ClearCache()
	assert.Equal(t, 0, evaluator.CacheSize())
}

func TestCELEvaluator_EvaluateBucketKey(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		attributes map[string]string
		want       string
		wantErr    bool
	}{
		{
			name:       "bucket_key with single attribute",
			expression: `bucket_key([attributes["region"]])`,
			attributes: map[string]string{"region": "us-east-1"},
			want:       "", // Will be a SHA256 hash
			wantErr:    false,
		},
		{
			name:       "bucket_key with multiple attributes",
			expression: `bucket_key([attributes["region"], attributes["category"]])`,
			attributes: map[string]string{
				"region":   "us-east-1",
				"category": "compute",
			},
			want:    "", // Will be a SHA256 hash
			wantErr: false,
		},
		{
			name:       "static string return",
			expression: `"static-bucket"`,
			attributes: map[string]string{},
			want:       "static-bucket",
			wantErr:    false,
		},
		{
			name:       "conditional expression",
			expression: `has(attributes.premium) && attributes.premium == "true" ? "premium-bucket" : "standard-bucket"`,
			attributes: map[string]string{"premium": "true"},
			want:       "premium-bucket",
			wantErr:    false,
		},
		{
			name:       "conditional expression - default path",
			expression: `has(attributes.premium) && attributes.premium == "true" ? "premium-bucket" : "standard-bucket"`,
			attributes: map[string]string{},
			want:       "standard-bucket",
			wantErr:    false,
		},
		{
			name:       "nil attributes handled as empty map",
			expression: `"bucket-" + (has(attributes.region) ? attributes.region : "default")`,
			attributes: nil,
			want:       "bucket-default",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			program, err := evaluator.CompileBucketKeyExpression(tt.expression)
			require.NoError(t, err)

			result, err := evaluator.EvaluateBucketKey(program, tt.attributes)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.want != "" {
					assert.Equal(t, tt.want, result)
				} else {
					// For bucket_key results, just verify it's a non-empty hash-like string
					assert.NotEmpty(t, result)
					assert.Len(t, result, 64) // SHA256 hex = 64 chars
				}
			}
		})
	}
}

func TestCELEvaluator_EvaluateBucketKey_Deterministic(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	expression := `bucket_key([attributes["region"], attributes["category"]])`
	attributes := map[string]string{
		"region":   "eu-west-1",
		"category": "storage",
	}

	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)

	// Evaluate multiple times
	results := make([]string, 10)
	for i := range results {
		result, err := evaluator.EvaluateBucketKey(program, attributes)
		require.NoError(t, err)
		results[i] = result
	}

	// All results should be identical
	for i := 1; i < len(results); i++ {
		assert.Equal(t, results[0], results[i], "bucket_key should be deterministic")
	}
}

func TestCELEvaluator_EvaluateBucketKey_NilProgram(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	_, err = evaluator.EvaluateBucketKey(nil, map[string]string{})
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCELEvaluation)
}

func TestCELEvaluator_EvaluateBucketKeyWithExpression(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	attributes := map[string]string{"key": "value"}

	// Valid expression
	result, err := evaluator.EvaluateBucketKeyWithExpression(`"test-" + attributes["key"]`, attributes)
	require.NoError(t, err)
	assert.Equal(t, "test-value", result)

	// Invalid expression
	_, err = evaluator.EvaluateBucketKeyWithExpression("", attributes)
	assert.Error(t, err)
}

func TestCELEvaluator_ValidateExpression(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	// Valid expression
	err = evaluator.ValidateExpression(`bucket_key([attributes["region"]])`)
	assert.NoError(t, err)

	// Invalid expression
	err = evaluator.ValidateExpression("")
	assert.Error(t, err)

	// Syntax error
	err = evaluator.ValidateExpression(`invalid_syntax(`)
	assert.Error(t, err)
}

func BenchmarkCELEvaluator_EvaluateBucketKey(b *testing.B) {
	evaluator, err := NewCELEvaluator()
	require.NoError(b, err)

	expression := `bucket_key([attributes["region"], attributes["category"], attributes["tier"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(b, err)

	attributes := map[string]string{
		"region":   "us-east-1",
		"category": "compute",
		"tier":     "standard",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		_, err := evaluator.EvaluateBucketKey(program, attributes)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCELEvaluator_EvaluateBucketKey_10k tests target of >10k/sec
func BenchmarkCELEvaluator_EvaluateBucketKey_10k(b *testing.B) {
	evaluator, err := NewCELEvaluator()
	require.NoError(b, err)

	expression := `bucket_key([attributes["region"], attributes["category"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(b, err)

	// Different attribute sets to simulate real workload
	attributeSets := []map[string]string{
		{"region": "us-east-1", "category": "compute"},
		{"region": "us-west-2", "category": "storage"},
		{"region": "eu-west-1", "category": "network"},
		{"region": "ap-south-1", "category": "database"},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := range b.N {
		attrs := attributeSets[i%len(attributeSets)]
		_, err := evaluator.EvaluateBucketKey(program, attrs)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Report ops/sec - target is >10k/sec
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "evaluations/sec")
}

func TestCELEvaluator_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	expression := `bucket_key([attributes["key"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)

	// Run concurrent evaluations
	const numGoroutines = 100
	const numIterations = 100

	errChan := make(chan error, numGoroutines)

	for range numGoroutines {
		go func() {
			for range numIterations {
				attrs := map[string]string{"key": "value"}
				_, err := evaluator.EvaluateBucketKey(program, attrs)
				if err != nil {
					errChan <- err
					return
				}
			}
			errChan <- nil
		}()
	}

	// Collect results
	for range numGoroutines {
		err := <-errChan
		assert.NoError(t, err)
	}
}

func TestCELEvaluator_ExpressionSecurityConstraints(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	// Expression that's too long
	longExpression := `"` + strings.Repeat("a", 5000) + `"`
	_, err = evaluator.CompileBucketKeyExpression(longExpression)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidCELExpression)
}
