package validation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuplicateChecker_Check_NoDuplicates(t *testing.T) {
	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	ctx := context.Background()

	// First check should not be a duplicate
	result, err := checker.Check(ctx, "measurement-1", 1)
	require.NoError(t, err)
	assert.False(t, result.IsDuplicate)

	// Second unique ID should not be a duplicate
	result, err = checker.Check(ctx, "measurement-2", 2)
	require.NoError(t, err)
	assert.False(t, result.IsDuplicate)
}

func TestDuplicateChecker_Check_WithinFileDuplicate(t *testing.T) {
	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	ctx := context.Background()

	// First occurrence
	result, err := checker.Check(ctx, "measurement-1", 10)
	require.NoError(t, err)
	assert.False(t, result.IsDuplicate)

	// Duplicate of first
	result, err = checker.Check(ctx, "measurement-1", 20)
	require.NoError(t, err)
	assert.True(t, result.IsDuplicate)
	assert.False(t, result.InDatabase)
	assert.Equal(t, 10, result.ExistingLineNumber)
}

func TestDuplicateChecker_Check_DatabaseDuplicate(t *testing.T) {
	// Mock database lookup that returns measurement-db-1 as existing
	dbLookup := func(_ context.Context, ids []string) (map[string]bool, error) {
		result := make(map[string]bool)
		for _, id := range ids {
			if id == "measurement-db-1" {
				result[id] = true
			}
		}
		return result, nil
	}

	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), dbLookup)

	// Preload the bloom filter with the database ID
	checker.PreloadFromDatabase(context.Background(), []string{"measurement-db-1"})

	ctx := context.Background()

	// Check should find database duplicate
	result, err := checker.Check(ctx, "measurement-db-1", 1)
	require.NoError(t, err)
	assert.True(t, result.IsDuplicate)
	assert.True(t, result.InDatabase)
}

func TestDuplicateChecker_Check_BloomFilterFalsePositive(t *testing.T) {
	// Mock database lookup that always returns false (not found)
	dbLookup := func(_ context.Context, _ []string) (map[string]bool, error) {
		return make(map[string]bool), nil
	}

	// Use a small bloom filter to increase false positive chance
	cfg := BloomFilterConfig{
		ExpectedItems:     10,
		FalsePositiveRate: 0.5, // High false positive rate for testing
	}

	checker := NewDuplicateChecker(cfg, dbLookup)

	ctx := context.Background()

	// Add many items to increase bloom filter saturation
	for i := 0; i < 100; i++ {
		_, err := checker.Check(ctx, "item-"+string(rune('a'+i%26))+string(rune('0'+i/26)), i)
		require.NoError(t, err)
	}

	// Stats should show we had some bloom filter activity
	stats := checker.Stats()
	assert.Greater(t, stats.UniqueIDs, 0)
}

func TestDuplicateChecker_CheckBatch(t *testing.T) {
	dbLookup := func(_ context.Context, ids []string) (map[string]bool, error) {
		result := make(map[string]bool)
		for _, id := range ids {
			if id == "existing-in-db" {
				result[id] = true
			}
		}
		return result, nil
	}

	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), dbLookup)
	checker.PreloadFromDatabase(context.Background(), []string{"existing-in-db"})

	rows := []ImportRow{
		{LineNumber: 1, MeasurementID: "unique-1"},
		{LineNumber: 2, MeasurementID: "unique-2"},
		{LineNumber: 3, MeasurementID: "existing-in-db"},
		{LineNumber: 4, MeasurementID: "unique-1"}, // Within-file duplicate
	}

	ctx := context.Background()
	results, err := checker.CheckBatch(ctx, rows)
	require.NoError(t, err)

	// unique-1 first occurrence - not duplicate
	assert.False(t, results[1].IsDuplicate)

	// unique-2 - not duplicate
	assert.False(t, results[2].IsDuplicate)

	// existing-in-db - database duplicate
	assert.True(t, results[3].IsDuplicate)
	assert.True(t, results[3].InDatabase)

	// unique-1 second occurrence - within-file duplicate
	assert.True(t, results[4].IsDuplicate)
	assert.Equal(t, 1, results[4].ExistingLineNumber)
}

func TestDuplicateChecker_Stats(t *testing.T) {
	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	ctx := context.Background()

	// Add some items
	_, _ = checker.Check(ctx, "item-1", 1)
	_, _ = checker.Check(ctx, "item-2", 2)
	_, _ = checker.Check(ctx, "item-1", 3) // Duplicate

	stats := checker.Stats()
	assert.Equal(t, 2, stats.UniqueIDs)
	assert.Equal(t, int64(1), stats.WithinFileDuplicates)
	assert.Equal(t, int64(1), stats.TruePositives)
}

func TestDuplicateChecker_Reset(t *testing.T) {
	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	ctx := context.Background()

	// Add items
	_, _ = checker.Check(ctx, "item-1", 1)
	_, _ = checker.Check(ctx, "item-2", 2)

	stats := checker.Stats()
	assert.Equal(t, 2, stats.UniqueIDs)

	// Reset
	checker.Reset(DefaultBloomFilterConfig())

	stats = checker.Stats()
	assert.Equal(t, 0, stats.UniqueIDs)
	assert.Equal(t, int64(0), stats.BloomHits)
	assert.Equal(t, int64(0), stats.BloomMisses)
}

func TestDuplicateChecker_FalsePositiveRate(t *testing.T) {
	checker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	// No hits yet
	rate := checker.FalsePositiveRate()
	assert.Equal(t, 0.0, rate)
}

func TestBloomFilterConfig_Defaults(t *testing.T) {
	cfg := DefaultBloomFilterConfig()
	assert.Equal(t, uint(1_000_000), cfg.ExpectedItems)
	assert.Equal(t, 0.01, cfg.FalsePositiveRate)
}
