// Package saga provides script similarity detection for tenant override validation.
//
// When tenants create custom overrides of platform saga definitions, the system
// validates that the override is meaningfully different from the platform default.
// This prevents tenants from creating "overrides" that are near-identical copies
// of platform scripts, which would create maintenance burden without benefit.
package saga

import (
	"errors"
	"strings"
)

// Similarity detection thresholds and errors.
var (
	// DefaultSimilarityThreshold is the maximum allowed similarity ratio (0.0-1.0)
	// between a tenant override and the platform default script.
	// Scripts with similarity above this threshold are rejected.
	DefaultSimilarityThreshold = 0.90

	// ErrScriptTooSimilar is returned when a tenant override script is too similar
	// to the platform default it replaces.
	ErrScriptTooSimilar = errors.New("override script is too similar to platform default")
)

// SimilarityResult holds the result of a script similarity comparison.
type SimilarityResult struct {
	// Ratio is the similarity ratio between 0.0 (completely different) and 1.0 (identical).
	Ratio float64

	// Distance is the raw Levenshtein edit distance.
	Distance int

	// MaxLength is the length of the longer string (used for ratio calculation).
	MaxLength int

	// TooSimilar is true when Ratio exceeds the threshold.
	TooSimilar bool
}

// ComputeSimilarity compares two scripts and returns a SimilarityResult.
// The comparison normalizes whitespace to avoid false positives from
// formatting-only differences.
//
// Similarity is calculated as: 1 - (levenshtein_distance / max_length).
// An empty override compared to a non-empty default yields ratio 0.0.
// Two empty scripts yield ratio 1.0.
func ComputeSimilarity(script1, script2 string) SimilarityResult {
	return ComputeSimilarityWithThreshold(script1, script2, DefaultSimilarityThreshold)
}

// ComputeSimilarityWithThreshold compares two scripts with a custom threshold.
func ComputeSimilarityWithThreshold(script1, script2 string, threshold float64) SimilarityResult {
	// Normalize whitespace for comparison
	norm1 := normalizeScript(script1)
	norm2 := normalizeScript(script2)

	// Handle edge cases
	if len(norm1) == 0 && len(norm2) == 0 {
		return SimilarityResult{
			Ratio:      1.0,
			Distance:   0,
			MaxLength:  0,
			TooSimilar: true,
		}
	}

	maxLen := len(norm1)
	if len(norm2) > maxLen {
		maxLen = len(norm2)
	}

	dist := levenshteinDistance(norm1, norm2)
	ratio := 1.0 - float64(dist)/float64(maxLen)

	return SimilarityResult{
		Ratio:      ratio,
		Distance:   dist,
		MaxLength:  maxLen,
		TooSimilar: ratio >= threshold,
	}
}

// normalizeScript normalizes a script for similarity comparison.
// Removes leading/trailing whitespace from each line and collapses
// multiple blank lines into one. This prevents formatting-only
// differences from inflating the distance.
func normalizeScript(script string) string {
	lines := strings.Split(script, "\n")
	normalized := make([]string, 0, len(lines))
	prevBlank := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevBlank {
				normalized = append(normalized, "")
				prevBlank = true
			}
			continue
		}
		prevBlank = false
		normalized = append(normalized, trimmed)
	}

	return strings.Join(normalized, "\n")
}

// levenshteinDistance computes the Levenshtein edit distance between two strings.
// Uses the standard dynamic programming approach with O(min(m,n)) space.
func levenshteinDistance(a, b string) int {
	la := len(a)
	lb := len(b)

	// Ensure a is the shorter string for space optimization
	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	// Use two rows instead of full matrix
	prev := make([]int, la+1)
	curr := make([]int, la+1)

	// Initialize first row
	for i := 0; i <= la; i++ {
		prev[i] = i
	}

	// Fill the matrix
	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[i] = min(
				prev[i]+1,      // deletion
				curr[i-1]+1,    // insertion
				prev[i-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[la]
}
