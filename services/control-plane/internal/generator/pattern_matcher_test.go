package generator_test

import (
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/meridianhub/meridian/cookbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
)

// realCookbookFS returns the embedded cookbook FS for integration-style tests.
func realCookbookFS() fs.FS {
	return cookbook.FS
}

// TestMatchPatterns_EnergySettlementMatch verifies that "EV charging UK energy" matches
// the energy-settlement pattern as the top result.
func TestMatchPatterns_EnergySettlementMatch(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "EV charging UK energy", "energy", 5)
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected at least one match for energy description")

	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}

	assert.Contains(t, names, "energy-settlement",
		"energy-settlement should appear in top results for energy description; got %v", names)

	// energy-settlement must outrank non-energy patterns.
	var energyIdx int
	for i, m := range matches {
		if m.Name == "energy-settlement" {
			energyIdx = i
			break
		}
	}
	// The energy-settlement pattern should be highly ranked (top 3).
	assert.LessOrEqual(t, energyIdx, 2,
		"energy-settlement should be in top 3 results; got index %d in %v", energyIdx, names)
}

// TestMatchPatterns_SaasBillingMatch verifies that "SaaS compute billing" matches
// the saas-billing pattern in the top results.
func TestMatchPatterns_SaasBillingMatch(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "SaaS compute billing", "saas", 5)
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected at least one match for SaaS description")

	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}

	assert.Contains(t, names, "saas-billing",
		"saas-billing should appear in results for SaaS compute billing description; got %v", names)

	// saas-billing should be highly ranked.
	var saasIdx int
	found := false
	for i, m := range matches {
		if m.Name == "saas-billing" {
			saasIdx = i
			found = true
			break
		}
	}
	require.True(t, found, "saas-billing must be present in results")
	assert.LessOrEqual(t, saasIdx, 2,
		"saas-billing should be in top 3 results; got index %d in %v", saasIdx, names)
}

// TestMatchPatterns_ScoreFields verifies that returned PatternMatch structs have
// populated Name, Title, Score, and Provides fields.
func TestMatchPatterns_ScoreFields(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "energy settlement billing", "energy", 3)
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	for _, m := range matches {
		assert.NotEmpty(t, m.Name, "Name should be non-empty")
		assert.NotEmpty(t, m.Title, "Title should be non-empty")
		assert.GreaterOrEqual(t, m.Score, float64(0), "Score should be non-negative")
	}
}

// TestMatchPatterns_ManifestFragmentLoaded verifies that ManifestFragment is populated.
func TestMatchPatterns_ManifestFragmentLoaded(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "EV charging UK energy", "energy", 5)
	require.NoError(t, err)

	for _, m := range matches {
		if m.Name == "energy-settlement" {
			assert.NotEmpty(t, m.ManifestFragment,
				"ManifestFragment should be populated for energy-settlement")
			assert.Contains(t, m.ManifestFragment, "KWH",
				"ManifestFragment should contain KWH instrument")
			return
		}
	}
	t.Fatal("energy-settlement not found in matches")
}

// TestMatchPatterns_SagaScriptLoaded verifies that SagaScript is populated for saga-bearing patterns.
func TestMatchPatterns_SagaScriptLoaded(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "EV charging UK energy", "energy", 5)
	require.NoError(t, err)

	for _, m := range matches {
		if m.Name == "energy-settlement" {
			assert.NotEmpty(t, m.SagaScript,
				"SagaScript should be populated for energy-settlement which has .star files")
			return
		}
	}
	t.Fatal("energy-settlement not found in matches")
}

// TestMatchPatterns_ConflictFiltering verifies that conflicting patterns are excluded.
// energy-a explicitly declares conflicts_with: [conflict-b].
// When energy-a is selected first (higher score), conflict-b must be excluded.
func TestMatchPatterns_ConflictFiltering(t *testing.T) {
	mockFS := buildConflictTestFS(t)

	// "kwh energy inventory" matches energy-a's provides (KWH, ENERGY_INVENTORY) directly.
	// conflict-b provides SOLAR_PANEL (no keyword overlap), so energy-a scores higher.
	matches, err := generator.MatchPatterns(mockFS, "kwh energy inventory settlement", "energy", 10)
	require.NoError(t, err)

	names := namesFromMatches(matches)

	// energy-a should be selected and conflict-b should be filtered.
	assert.Contains(t, names, "energy-a", "energy-a should be selected")
	assert.NotContains(t, names, "conflict-b",
		"conflict-b should be filtered because energy-a conflicts_with it; got %v", names)
}

// TestMatchPatterns_ExtendsResolution verifies that base patterns required via extends
// are included in the results even if not explicitly matched.
func TestMatchPatterns_ExtendsResolution(t *testing.T) {
	// saas-billing extends base-fiat-usd. When saas-billing matches, base-fiat-usd
	// should appear in results even though it has no industry or keyword overlap.
	matches, err := generator.MatchPatterns(realCookbookFS(), "SaaS compute billing GPU hours", "saas", 10)
	require.NoError(t, err)

	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}

	assert.Contains(t, names, "saas-billing",
		"saas-billing should be in results; got %v", names)
	assert.Contains(t, names, "base-fiat-usd",
		"base-fiat-usd should be resolved via saas-billing extends; got %v", names)
}

// TestMatchPatterns_MaxResults verifies that at most maxResults patterns are returned.
func TestMatchPatterns_MaxResults(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "billing energy compute payments", "", 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(matches), 3, "should return at most 3 results")
}

// TestMatchPatterns_ZeroMaxResults verifies that 0 maxResults returns all matches.
func TestMatchPatterns_ZeroMaxResults(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "energy billing", "", 0)
	require.NoError(t, err)
	assert.Greater(t, len(matches), 3, "with maxResults=0 should return more than 3 patterns")
}

// TestMatchPatterns_EmptyDescription returns results ordered by registry order (no scoring boost).
func TestMatchPatterns_EmptyDescription(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "", "", 5)
	require.NoError(t, err)
	// Should still return patterns even with no description.
	assert.NotEmpty(t, matches, "should return patterns even with empty description")
}

// TestMatchPatterns_InvalidFS returns an error for an FS missing registry.json.
func TestMatchPatterns_InvalidFS(t *testing.T) {
	emptyFS := fstest.MapFS{}
	_, err := generator.MatchPatterns(emptyFS, "energy", "energy", 5)
	assert.Error(t, err, "should return error when registry.json is missing")
}

// TestMatchPatterns_ProvidesPopulated verifies that Provides is populated correctly.
func TestMatchPatterns_ProvidesPopulated(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "EV charging UK energy", "energy", 5)
	require.NoError(t, err)

	for _, m := range matches {
		if m.Name == "energy-settlement" {
			assert.Contains(t, m.Provides, "KWH",
				"energy-settlement Provides should contain KWH instrument")
			assert.Contains(t, m.Provides, "usage_to_value",
				"energy-settlement Provides should contain usage_to_value saga")
			return
		}
	}
	t.Fatal("energy-settlement not found in matches")
}

// TestMatchPatterns_RequiresPopulated verifies that Requires is populated correctly.
func TestMatchPatterns_RequiresPopulated(t *testing.T) {
	matches, err := generator.MatchPatterns(realCookbookFS(), "EV charging UK energy", "energy", 5)
	require.NoError(t, err)

	for _, m := range matches {
		if m.Name == "energy-settlement" {
			assert.Contains(t, m.Requires, "GBP",
				"energy-settlement Requires should contain GBP")
			return
		}
	}
	t.Fatal("energy-settlement not found in matches")
}

// TestMatchPatterns_IndustryBoost verifies that matching industry boosts score significantly.
func TestMatchPatterns_IndustryBoost(t *testing.T) {
	// With explicit industry match, the relevant pattern should score higher than without.
	matchesWithIndustry, err := generator.MatchPatterns(realCookbookFS(), "billing", "saas", 5)
	require.NoError(t, err)

	matchesNoIndustry, err := generator.MatchPatterns(realCookbookFS(), "billing", "", 5)
	require.NoError(t, err)

	// Find saas-billing score in both result sets.
	scoreWith := float64(-1)
	for _, m := range matchesWithIndustry {
		if m.Name == "saas-billing" {
			scoreWith = m.Score
			break
		}
	}
	scoreWithout := float64(-1)
	for _, m := range matchesNoIndustry {
		if m.Name == "saas-billing" {
			scoreWithout = m.Score
			break
		}
	}

	if scoreWith >= 0 && scoreWithout >= 0 {
		assert.Greater(t, scoreWith, scoreWithout,
			"saas-billing should score higher when industry=saas is specified")
	}
}

// --- helpers ---

// buildConflictTestFS creates a minimal fstest.MapFS with two patterns:
// energy-a (conflicts_with: [conflict-b]) and conflict-b.
func buildConflictTestFS(t *testing.T) fs.FS {
	t.Helper()

	energyA := map[string]interface{}{
		"name":  "energy-a",
		"type":  "registry:pattern",
		"title": "Energy A",
		"meta": map[string]interface{}{
			"industries": []string{"energy"},
			"provides": map[string]interface{}{
				"instruments":   []string{"KWH"},
				"account_types": []string{"ENERGY_INVENTORY"},
				"sagas":         []string{},
			},
			"requires":       map[string]interface{}{"instruments": []string{}, "market_data": []string{}},
			"composes_with":  []string{},
			"conflicts_with": []string{"conflict-b"},
			"extends":        []string{},
		},
	}

	conflictB := map[string]interface{}{
		"name":  "conflict-b",
		"type":  "registry:pattern",
		"title": "Conflict B (incompatible flat rate)",
		"meta": map[string]interface{}{
			"industries": []string{},
			"provides": map[string]interface{}{
				"instruments":   []string{"SOLAR_PANEL"},
				"account_types": []string{"FLAT_RATE"},
				"sagas":         []string{},
			},
			"requires":       map[string]interface{}{"instruments": []string{}, "market_data": []string{}},
			"composes_with":  []string{},
			"conflicts_with": []string{},
			"extends":        []string{},
		},
	}

	reg := map[string]interface{}{
		"name": "test-registry",
		"items": []interface{}{
			map[string]interface{}{"name": "energy-a", "type": "registry:pattern", "title": "Energy A"},
			map[string]interface{}{"name": "conflict-b", "type": "registry:pattern", "title": "Conflict B"},
		},
	}

	regData, err := json.Marshal(reg)
	require.NoError(t, err)
	energyAData, err := json.Marshal(energyA)
	require.NoError(t, err)
	conflictBData, err := json.Marshal(conflictB)
	require.NoError(t, err)

	return fstest.MapFS{
		"registry.json":                              {Data: regData},
		"patterns/energy-a/pattern.json":             {Data: energyAData},
		"patterns/energy-a/manifest-fragment.yaml":   {Data: []byte("instruments:\n  - code: KWH\n")},
		"patterns/conflict-b/pattern.json":           {Data: conflictBData},
		"patterns/conflict-b/manifest-fragment.yaml": {Data: []byte("instruments:\n  - code: ELECTRICITY\n")},
	}
}

// namesFromMatches extracts pattern names for readability in assertion messages.
func namesFromMatches(matches []generator.PatternMatch) []string {
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Name
	}
	return names
}
