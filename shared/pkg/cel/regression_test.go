package cel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CEL Version Regression Tests
//
// These tests store expected bucket_id values for known attribute sets.
// If a cel-go upgrade changes the hashing behavior, these tests will fail,
// alerting us to a breaking change that could corrupt existing data.
//
// IMPORTANT: If these tests fail after a cel-go upgrade:
// 1. Do NOT update the expected values without careful consideration
// 2. A hash change means existing bucket_ids in the database will no longer match
// 3. This requires a data migration strategy before upgrading
//
// Current cel-go version: 0.26.1

// GoldenBucketKey represents a known input/output pair for regression testing.
type GoldenBucketKey struct {
	Name       string
	Expression string
	Attributes map[string]string
	Expected   string // SHA256 hex hash
}

// goldenBucketKeys contains pre-computed bucket_key results that must remain stable.
// These values were computed with cel-go v0.26.1 and MUST NOT change across versions.
var goldenBucketKeys = []GoldenBucketKey{
	{
		Name:       "single_attribute_region_US",
		Expression: `bucket_key([attributes["region"]])`,
		Attributes: map[string]string{"region": "US"},
		Expected:   "8a3134c4f95c88e66925ee9b0232ffd936607043e6f3a13588215a4f69adf600",
	},
	{
		Name:       "two_attributes_type_grade",
		Expression: `bucket_key([attributes["type"], attributes["grade"]])`,
		Attributes: map[string]string{"type": "carbon", "grade": "A"},
		Expected:   "95d2fd305dfd7ca98eada22bd120b526f1cf3946fdc61c88327ca96aadb47e75",
	},
	{
		Name:       "three_attributes_region_vintage_source",
		Expression: `bucket_key([attributes["region"], attributes["vintage"], attributes["source"]])`,
		Attributes: map[string]string{"region": "EU", "vintage": "2024", "source": "solar"},
		Expected:   "8dfe1d2abeaa6234732ad3bef06d6e621533a13567bd46b5c86e174e0678f175",
	},
	{
		Name:       "empty_string_values",
		Expression: `bucket_key([attributes["a"], attributes["b"]])`,
		Attributes: map[string]string{"a": "", "b": ""},
		Expected:   "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc",
	},
	{
		Name:       "unicode_attributes",
		Expression: `bucket_key([attributes["name"], attributes["region"]])`,
		Attributes: map[string]string{"name": "日本", "region": "アジア"},
		Expected:   "1b93ed4552e2cde6d37be7f80d531272f029dfffdb221a215599353c00fc0b94",
	},
	{
		Name:       "special_characters",
		Expression: `bucket_key([attributes["path"], attributes["id"]])`,
		Attributes: map[string]string{"path": "/usr/local/bin", "id": "abc-123_456"},
		Expected:   "4e14daf457c2a74b95b798f0794ba62af5101e40eb3e3fcc5e66c802dc5ff57f",
	},
	{
		Name:       "numeric_string_values",
		Expression: `bucket_key([attributes["year"], attributes["month"], attributes["day"]])`,
		Attributes: map[string]string{"year": "2024", "month": "12", "day": "25"},
		Expected:   "19b1bed3f813050fc4bac5cece3dcf9c2b20efd03adde8f5a675c13b1502bc56",
	},
}

// TestBucketKeyVersionRegression verifies that bucket_key produces the same
// hashes as when these tests were written (cel-go v0.26.1).
//
// CRITICAL: If this test fails after upgrading cel-go:
// - The hash algorithm or length-prefixed encoding has changed
// - All existing bucket_ids in production databases are now invalid
// - A migration plan is required before deploying
func TestBucketKeyVersionRegression(t *testing.T) {
	// Document the version being tested
	t.Logf("Testing bucket_key regression against cel-go version: %s", CELVersion)

	c, err := NewCompiler()
	require.NoError(t, err)

	// Verify all golden values match expected hashes
	for _, golden := range goldenBucketKeys {
		t.Run(golden.Name, func(t *testing.T) {
			prg, err := c.CompileBucketKey(golden.Expression)
			require.NoError(t, err, "Failed to compile expression")

			input := map[string]any{"attributes": golden.Attributes}
			result, _, err := prg.Eval(input)
			require.NoError(t, err, "Failed to evaluate expression")

			actualHash := result.Value().(string)

			// This is the critical assertion - if this fails after a cel-go upgrade,
			// it means bucket_key hashes have changed and existing data is incompatible
			assert.Equal(t, golden.Expected, actualHash,
				"REGRESSION DETECTED: bucket_key hash changed for %s.\n"+
					"Expected: %s\n"+
					"Actual:   %s\n"+
					"This indicates a breaking change in cel-go hash computation.\n"+
					"All existing bucket_ids in production are now invalid.",
				golden.Name, golden.Expected, actualHash)

			t.Logf("✓ %s: %s", golden.Name, actualHash[:16]+"...")
		})
	}
}

// TestBucketKeyRegressionStability runs the same evaluation multiple times
// and across compiler instances to ensure consistent results.
func TestBucketKeyRegressionStability(t *testing.T) {
	// Create first compiler
	c1, err := NewCompiler()
	require.NoError(t, err)

	// Create second compiler (separate instance)
	c2, err := NewCompiler()
	require.NoError(t, err)

	expression := `bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`
	attrs := map[string]string{"type": "energy", "region": "NA", "vintage": "2023"}
	input := map[string]any{"attributes": attrs}

	prg1, err := c1.CompileBucketKey(expression)
	require.NoError(t, err)

	prg2, err := c2.CompileBucketKey(expression)
	require.NoError(t, err)

	// Get results from both programs
	result1, _, err := prg1.Eval(input)
	require.NoError(t, err)

	result2, _, err := prg2.Eval(input)
	require.NoError(t, err)

	hash1 := result1.Value().(string)
	hash2 := result2.Value().(string)

	assert.Equal(t, hash1, hash2, "Different compiler instances should produce identical hashes")
	t.Logf("Cross-compiler hash verification passed: %s", hash1)
}

// TestValidationExpressionRegressionStability verifies that validation
// expressions produce consistent boolean results.
func TestValidationExpressionRegressionStability(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	now := time.Now()
	oneHourLater := now.Add(time.Hour)

	tests := []struct {
		name       string
		expression string
		input      map[string]any
		expected   bool
	}{
		{
			name:       "attribute_equality",
			expression: `attributes["region"] == "US"`,
			input: map[string]any{
				"attributes": map[string]string{"region": "US"},
				"amount":     "100",
				"valid_from": now,
				"valid_to":   oneHourLater,
				"source":     "test",
			},
			expected: true,
		},
		{
			name:       "amount_comparison",
			expression: `parse_decimal(amount) > 50.0`,
			input: map[string]any{
				"attributes": map[string]string{},
				"amount":     "100.5",
				"valid_from": now,
				"valid_to":   oneHourLater,
				"source":     "test",
			},
			expected: true,
		},
		{
			name:       "timestamp_comparison",
			expression: `valid_from < valid_to`,
			input: map[string]any{
				"attributes": map[string]string{},
				"amount":     "0",
				"valid_from": now,
				"valid_to":   oneHourLater,
				"source":     "test",
			},
			expected: true,
		},
		{
			name:       "source_not_empty",
			expression: `source != ""`,
			input: map[string]any{
				"attributes": map[string]string{},
				"amount":     "0",
				"valid_from": now,
				"valid_to":   oneHourLater,
				"source":     "meter-001",
			},
			expected: true,
		},
		{
			name:       "complex_validation",
			expression: `attributes["type"] in ["A", "B", "C"] && parse_int(attributes["priority"]) >= 1`,
			input: map[string]any{
				"attributes": map[string]string{"type": "B", "priority": "5"},
				"amount":     "0",
				"valid_from": now,
				"valid_to":   oneHourLater,
				"source":     "test",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			// Run multiple times to ensure stability
			for i := 0; i < 100; i++ {
				result, _, err := prg.Eval(tt.input)
				require.NoError(t, err, "iteration %d", i)
				assert.Equal(t, tt.expected, result.Value(), "iteration %d", i)
			}
		})
	}
}

// TestCELVersionConstant verifies that the CELVersion constant is set correctly.
// This test should be updated when upgrading cel-go.
func TestCELVersionConstant(t *testing.T) {
	// This should match the version in go.mod
	assert.Equal(t, "0.26.1", CELVersion, "CELVersion constant should match go.mod")

	// Log for audit trail
	t.Logf("Tested against CEL version: %s", CELVersion)
	t.Logf("If upgrading cel-go, run all regression tests first!")
}
