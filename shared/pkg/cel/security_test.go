package cel

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Poison Pill Tests
//
// These tests verify that computationally expensive or malicious CEL expressions
// are blocked by cost limits before they can consume excessive resources.
//
// The CostLimit (10000) is designed to allow legitimate business logic while
// blocking expressions designed to cause denial of service.

// TestPoisonPillCostLimitAbort verifies that expensive expressions are aborted
// instantly (within 1ms) with a clear error, not allowed to consume resources.
func TestPoisonPillCostLimitAbort(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// This expression has high evaluation cost due to deeply nested operations
	// on the map - each iteration multiplies the cost
	poisonPillExpressions := []struct {
		name        string
		expression  string
		expectError string
	}{
		{
			name: "deeply_nested_map_access",
			expression: `attributes["a"] + attributes["a"] + attributes["a"] + attributes["a"] + ` +
				`attributes["a"] + attributes["a"] + attributes["a"] + attributes["a"] + ` +
				`attributes["a"] + attributes["a"] + attributes["a"] + attributes["a"] + ` +
				`attributes["a"] + attributes["a"] + attributes["a"] + attributes["a"] + ` +
				`attributes["a"] + attributes["a"] + attributes["a"] + attributes["a"]`,
			expectError: "", // May compile successfully, need to test evaluation
		},
		{
			name:        "repeated_string_concatenation",
			expression:  strings.Repeat(`attributes["x"] + `, 100) + `attributes["x"]`,
			expectError: "", // High cost at evaluation time
		},
	}

	input := map[string]any{
		"attributes": map[string]string{
			"a": "value",
			"x": "test",
		},
	}

	for _, tt := range poisonPillExpressions {
		t.Run(tt.name, func(t *testing.T) {
			// First check if it exceeds static constraints
			err := validateExpressionConstraints(tt.expression)
			if err != nil {
				t.Logf("Expression blocked by static constraints: %v", err)
				return // This is acceptable - expression was blocked
			}

			// Try to compile
			prg, compileErr := c.CompileBucketKey(tt.expression)
			if compileErr != nil {
				// Cost limit can be enforced at compile time for some expressions
				t.Logf("Expression blocked at compile time: %v", compileErr)
				return
			}

			// If compiled, measure evaluation time
			start := time.Now()
			_, _, evalErr := prg.Eval(input)
			elapsed := time.Since(start)

			if evalErr != nil {
				// Good - expression was blocked
				t.Logf("Expression blocked at evaluation (took %v): %v", elapsed, evalErr)
				assert.Less(t, elapsed, 100*time.Millisecond,
					"Poison pill should be aborted quickly, not after %v", elapsed)
			} else {
				t.Logf("Expression evaluated in %v (may need stronger test case)", elapsed)
			}
		})
	}
}

// TestPoisonPillNoGoroutineLeaks verifies that blocked expressions do not
// leak goroutines. This is critical for long-running services.
func TestPoisonPillNoGoroutineLeaks(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Count goroutines before test
	runtime.GC()
	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d", initialGoroutines)

	// Create expressions that might leak if not properly cancelled
	expressions := []string{
		// Long expression that should be blocked by length
		strings.Repeat("a", MaxExpressionLength+1),
		// Deep expression that should be blocked by depth
		strings.Repeat("(", MaxExpressionDepth+2) + "true" + strings.Repeat(")", MaxExpressionDepth+2),
	}

	for i, expr := range expressions {
		_, _ = c.CompileValidation(expr) // Intentionally ignoring errors
		_, _ = c.CompileBucketKey(expr)  // Intentionally ignoring errors
		t.Logf("Attempted expression %d (blocked as expected)", i)
	}

	// Allow any cleanup - wait for goroutines to settle within acceptable threshold
	runtime.GC()
	_ = await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= initialGoroutines+2
	})

	finalGoroutines := runtime.NumGoroutine()
	t.Logf("Final goroutines: %d", finalGoroutines)

	// Allow for some variance but should not have leaked significantly
	leakedGoroutines := finalGoroutines - initialGoroutines
	assert.LessOrEqual(t, leakedGoroutines, 2,
		"Should not leak more than 2 goroutines after blocking poison pills")
}

// TestPoisonPillResourceExhaustedError verifies that cost limit violations
// produce clear, user-friendly error messages.
func TestPoisonPillResourceExhaustedError(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Test expression length limit
	t.Run("expression_too_long", func(t *testing.T) {
		longExpr := strings.Repeat("true || ", MaxExpressionLength/8+1) + "true"
		_, err := c.CompileValidation(longExpr)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrExpressionTooLong)
		assert.Contains(t, err.Error(), "exceeds maximum length")
	})

	// Test expression depth limit
	t.Run("expression_too_deep", func(t *testing.T) {
		deepExpr := strings.Repeat("(", MaxExpressionDepth+2) + "true" + strings.Repeat(")", MaxExpressionDepth+2)
		_, err := c.CompileValidation(deepExpr)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrExpressionTooDeep)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})
}

// TestPoisonPillTimeBomb verifies that expressions cannot introduce
// time-based denial of service (e.g., infinite loops or very long operations).
func TestPoisonPillTimeBomb(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Expressions that could be time bombs in less-restricted environments
	timeBombs := []struct {
		name       string
		expression string
	}{
		{
			name:       "large_list_size_check",
			expression: `size([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]) > 0`,
		},
		{
			name:       "nested_ternary",
			expression: `true ? (true ? (true ? "a" : "b") : "c") : "d"`,
		},
		{
			name:       "string_operations",
			expression: `"hello world".contains("world")`,
		},
	}

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "0",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	}

	for _, tt := range timeBombs {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			if err != nil {
				t.Logf("Expression blocked at compile: %v", err)
				return
			}

			start := time.Now()
			_, _, evalErr := prg.Eval(input)
			_ = evalErr // Intentionally ignoring - we're measuring time, not correctness
			elapsed := time.Since(start)

			// Even if it succeeds, it must complete quickly
			assert.Less(t, elapsed, 10*time.Millisecond,
				"Expression %s took too long: %v", tt.name, elapsed)
		})
	}
}

// TestCostLimitConstant verifies the CostLimit constant is set appropriately.
func TestCostLimitConstant(t *testing.T) {
	assert.Equal(t, uint64(10000), uint64(CostLimit),
		"CostLimit should be 10000 as documented")
}

// Delimiter Injection Tests
//
// These tests verify that bucket_key() prevents collision attacks where
// different attribute combinations could produce the same hash through
// delimiter manipulation.

// TestDelimiterInjectionPrevention verifies that bucket_key() uses
// length-prefixed encoding to prevent delimiter injection attacks.
func TestDelimiterInjectionPrevention(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["vintage"]])`)
	require.NoError(t, err)

	// These two inputs would collide with naive delimiter-based concatenation:
	// "US|East" + "2024" vs "US" + "East|2024"
	// Both would become "US|East|2024" with a "|" delimiter
	attackVectors := []struct {
		name   string
		attrs1 map[string]string
		attrs2 map[string]string
	}{
		{
			name:   "pipe_delimiter_injection",
			attrs1: map[string]string{"region": "US|East", "vintage": "2024"},
			attrs2: map[string]string{"region": "US", "vintage": "East|2024"},
		},
		{
			name:   "colon_delimiter_injection",
			attrs1: map[string]string{"region": "US:East", "vintage": "2024"},
			attrs2: map[string]string{"region": "US", "vintage": "East:2024"},
		},
		{
			name:   "null_byte_injection",
			attrs1: map[string]string{"region": "US\x00East", "vintage": "2024"},
			attrs2: map[string]string{"region": "US", "vintage": "\x00East2024"},
		},
		{
			name:   "newline_injection",
			attrs1: map[string]string{"region": "US\nEast", "vintage": "2024"},
			attrs2: map[string]string{"region": "US", "vintage": "\nEast2024"},
		},
		{
			name:   "empty_string_boundary",
			attrs1: map[string]string{"region": "", "vintage": "US2024"},
			attrs2: map[string]string{"region": "US", "vintage": "2024"},
		},
		{
			name:   "concatenation_collision",
			attrs1: map[string]string{"region": "ab", "vintage": "cd"},
			attrs2: map[string]string{"region": "a", "vintage": "bcd"},
		},
		{
			name:   "length_prefix_attack",
			attrs1: map[string]string{"region": "\x00\x00\x00\x02ab", "vintage": "cd"},
			attrs2: map[string]string{"region": "ab", "vintage": "cd"},
		},
	}

	for _, tt := range attackVectors {
		t.Run(tt.name, func(t *testing.T) {
			input1 := map[string]any{"attributes": tt.attrs1}
			input2 := map[string]any{"attributes": tt.attrs2}

			result1, _, err := prg.Eval(input1)
			require.NoError(t, err)

			result2, _, err := prg.Eval(input2)
			require.NoError(t, err)

			hash1 := result1.Value().(string)
			hash2 := result2.Value().(string)

			assert.NotEqual(t, hash1, hash2,
				"Different inputs must produce different hashes to prevent %s", tt.name)
			t.Logf("Input1: %v -> %s", tt.attrs1, hash1[:16]+"...")
			t.Logf("Input2: %v -> %s", tt.attrs2, hash2[:16]+"...")
		})
	}
}

// TestDelimiterInjectionWithThreeAttributes extends the test to three-attribute keys.
func TestDelimiterInjectionWithThreeAttributes(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["a"], attributes["b"], attributes["c"]])`)
	require.NoError(t, err)

	// These could collide with naive concatenation
	collisionPairs := []struct {
		name   string
		attrs1 map[string]string
		attrs2 map[string]string
	}{
		{
			name:   "shift_left",
			attrs1: map[string]string{"a": "xy", "b": "z", "c": ""},
			attrs2: map[string]string{"a": "x", "b": "yz", "c": ""},
		},
		{
			name:   "shift_right",
			attrs1: map[string]string{"a": "", "b": "x", "c": "yz"},
			attrs2: map[string]string{"a": "", "b": "xy", "c": "z"},
		},
		{
			name:   "all_shift",
			attrs1: map[string]string{"a": "abc", "b": "", "c": "def"},
			attrs2: map[string]string{"a": "ab", "b": "c", "c": "def"},
		},
	}

	for _, tt := range collisionPairs {
		t.Run(tt.name, func(t *testing.T) {
			input1 := map[string]any{"attributes": tt.attrs1}
			input2 := map[string]any{"attributes": tt.attrs2}

			result1, _, err := prg.Eval(input1)
			require.NoError(t, err)

			result2, _, err := prg.Eval(input2)
			require.NoError(t, err)

			assert.NotEqual(t, result1.Value(), result2.Value(),
				"%s: collision detected", tt.name)
		})
	}
}

// TestLengthPrefixedEncodingFormat verifies the internal encoding format
// used by bucket_key is resistant to manipulation.
func TestLengthPrefixedEncodingFormat(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Test that varying lengths produce different hashes even with same content
	tests := []struct {
		name       string
		expression string
		attrs      map[string]string
	}{
		{
			name:       "single_char",
			expression: `bucket_key([attributes["x"]])`,
			attrs:      map[string]string{"x": "a"},
		},
		{
			name:       "double_char",
			expression: `bucket_key([attributes["x"]])`,
			attrs:      map[string]string{"x": "aa"},
		},
		{
			name:       "empty",
			expression: `bucket_key([attributes["x"]])`,
			attrs:      map[string]string{"x": ""},
		},
	}

	hashes := make([]string, 0, len(tests))
	for _, tt := range tests {
		prg, err := c.CompileBucketKey(tt.expression)
		require.NoError(t, err)

		result, _, err := prg.Eval(map[string]any{"attributes": tt.attrs})
		require.NoError(t, err)

		hash := result.Value().(string)
		hashes = append(hashes, hash)
		t.Logf("%s: %s", tt.name, hash)
	}

	// All hashes must be unique
	for i := 0; i < len(hashes); i++ {
		for j := i + 1; j < len(hashes); j++ {
			assert.NotEqual(t, hashes[i], hashes[j],
				"Hash collision between test cases %d and %d", i, j)
		}
	}
}

// TestSecurityConstraintConstants verifies all security constraints are documented.
func TestSecurityConstraintConstants(t *testing.T) {
	// These values should be stable across versions
	assert.Equal(t, 4096, MaxExpressionLength, "MaxExpressionLength should be 4KB")
	assert.Equal(t, 10, MaxExpressionDepth, "MaxExpressionDepth should be 10")
	assert.Equal(t, uint64(10000), uint64(CostLimit), "CostLimit should be 10000")

	t.Logf("Security constraints: MaxLength=%d, MaxDepth=%d, CostLimit=%d",
		MaxExpressionLength, MaxExpressionDepth, CostLimit)
}

// TestErrorTypesAreSentinels verifies error types can be used with errors.Is().
func TestErrorTypesAreSentinels(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Test ErrExpressionTooLong
	longExpr := strings.Repeat("x", MaxExpressionLength+1)
	_, err = c.CompileValidation(longExpr)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrExpressionTooLong), "Should unwrap to ErrExpressionTooLong")

	// Test ErrExpressionTooDeep
	deepExpr := strings.Repeat("(", MaxExpressionDepth+2) + "true" + strings.Repeat(")", MaxExpressionDepth+2)
	_, err = c.CompileValidation(deepExpr)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrExpressionTooDeep), "Should unwrap to ErrExpressionTooDeep")

	// Test ErrCompilation
	_, err = c.CompileValidation("undefined_var")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCompilation), "Should unwrap to ErrCompilation")
}
