package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{name: "identical strings", a: "abc", b: "abc", expected: 0},
		{name: "empty strings", a: "", b: "", expected: 0},
		{name: "one empty", a: "abc", b: "", expected: 3},
		{name: "other empty", a: "", b: "abc", expected: 3},
		{name: "single substitution", a: "abc", b: "aXc", expected: 1},
		{name: "single insertion", a: "abc", b: "abXc", expected: 1},
		{name: "single deletion", a: "abXc", b: "abc", expected: 1},
		{name: "kitten to sitting", a: "kitten", b: "sitting", expected: 3},
		{name: "completely different", a: "abcdef", b: "ghijkl", expected: 6},
		{name: "single char", a: "a", b: "b", expected: 1},
		{name: "same single char", a: "a", b: "a", expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := levenshteinDistance(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLevenshteinDistance_Symmetry(t *testing.T) {
	// Levenshtein distance should be symmetric
	pairs := [][2]string{
		{"hello", "world"},
		{"abc", "def"},
		{"test", "testing"},
	}

	for _, pair := range pairs {
		t.Run(pair[0]+"_"+pair[1], func(t *testing.T) {
			d1 := levenshteinDistance(pair[0], pair[1])
			d2 := levenshteinDistance(pair[1], pair[0])
			assert.Equal(t, d1, d2, "levenshtein distance should be symmetric")
		})
	}
}

func TestNormalizeScript(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trims line whitespace",
			input:    "  abc  \n  def  ",
			expected: "abc\ndef",
		},
		{
			name:     "collapses blank lines",
			input:    "abc\n\n\n\ndef",
			expected: "abc\n\ndef",
		},
		{
			name:     "handles tabs",
			input:    "\tabc\t\n\tdef\t",
			expected: "abc\ndef",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only whitespace",
			input:    "   \n   \n   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeScript(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestComputeSimilarity(t *testing.T) {
	t.Run("identical scripts are 100% similar", func(t *testing.T) {
		script := "def posting_rules(ctx):\n    return ['step1', 'step2']"
		result := ComputeSimilarity(script, script)
		assert.Equal(t, 1.0, result.Ratio)
		assert.Equal(t, 0, result.Distance)
		assert.True(t, result.TooSimilar, "identical scripts should be flagged as too similar")
	})

	t.Run("completely different scripts are 0% similar", func(t *testing.T) {
		script1 := "aaaaaaaaaa"
		script2 := "bbbbbbbbbb"
		result := ComputeSimilarity(script1, script2)
		assert.Equal(t, 0.0, result.Ratio)
		assert.False(t, result.TooSimilar)
	})

	t.Run("two empty scripts are identical", func(t *testing.T) {
		result := ComputeSimilarity("", "")
		assert.Equal(t, 1.0, result.Ratio)
		assert.True(t, result.TooSimilar)
	})

	t.Run("empty vs non-empty is 0% similar", func(t *testing.T) {
		result := ComputeSimilarity("", "def posting_rules(ctx): pass")
		assert.Equal(t, 0.0, result.Ratio)
		assert.False(t, result.TooSimilar)
	})

	t.Run("95% similar script is rejected at default threshold", func(t *testing.T) {
		// Create a script where only 5% is different - nearly identical
		base := "def posting_rules(ctx):\n    return ['step1', 'step2', 'step3', 'step4', 'step5', 'step6', 'step7', 'step8', 'step9', 'step10']"
		modified := "def posting_rules(ctx):\n    return ['step1', 'step2', 'step3', 'step4', 'step5', 'step6', 'step7', 'step8', 'step9', 'stepAA']" // Changed last step
		result := ComputeSimilarity(base, modified)

		// With only 2 chars changed out of ~120, similarity should be very high
		assert.True(t, result.Ratio > 0.90, "scripts with minor changes should be > 90%% similar, got %.4f", result.Ratio)
		assert.True(t, result.TooSimilar, "nearly identical scripts should be flagged")
	})

	t.Run("50% similar script is allowed at default threshold", func(t *testing.T) {
		script1 := "def posting_rules(ctx):\n    return ['step1', 'step2']"
		script2 := "def execute(ctx):\n    result = validate(ctx)\n    return process(result)"
		result := ComputeSimilarity(script1, script2)

		assert.True(t, result.Ratio < 0.90, "significantly different scripts should be < 90%% similar, got %.4f", result.Ratio)
		assert.False(t, result.TooSimilar, "significantly different scripts should be allowed")
	})

	t.Run("whitespace-only differences are normalized", func(t *testing.T) {
		script1 := "def foo():\n    return 1"
		script2 := "def foo():\n        return 1" // More indentation
		result := ComputeSimilarity(script1, script2)

		assert.Equal(t, 1.0, result.Ratio, "whitespace-only differences should be normalized to identical")
	})
}

func TestComputeSimilarityWithThreshold(t *testing.T) {
	t.Run("custom threshold 0.80", func(t *testing.T) {
		script1 := "def posting_rules(ctx):\n    return ['step1', 'step2', 'step3']"
		// Change ~15% of the script - small but meaningful change
		script2 := "def posting_rules(ctx):\n    return ['stepA', 'stepB', 'step3']"
		result := ComputeSimilarityWithThreshold(script1, script2, 0.80)

		assert.True(t, result.Ratio > 0.80, "scripts with ~15%% changes should be above 80%% threshold")
		assert.True(t, result.TooSimilar, "should be flagged at 80%% threshold")
	})

	t.Run("custom threshold 0.99 only rejects near-identical", func(t *testing.T) {
		script1 := "def posting_rules(ctx):\n    return ['step1', 'step2', 'step3']"
		script2 := "def posting_rules(ctx):\n    return ['step1', 'step2', 'step4']"
		result := ComputeSimilarityWithThreshold(script1, script2, 0.99)

		// Small change should pass strict threshold
		require.True(t, result.Ratio < 0.99)
		assert.False(t, result.TooSimilar, "small changes should pass at 99%% threshold")
	})
}
