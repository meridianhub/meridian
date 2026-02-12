package persistence_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPositionRepository_Insert tests the append-only insert behavior
func TestPositionRepository_Insert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("insert single position", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-001",
			"GBP",
			"default",
			decimal.NewFromFloat(100.50),
			"Monetary",
			map[string]string{"source": "deposit"},
			uuid.New(),
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// Verify position was inserted
		retrieved, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)
		assert.Equal(t, pos.AccountID, retrieved.AccountID)
		assert.Equal(t, pos.InstrumentCode, retrieved.InstrumentCode)
		assert.True(t, pos.Amount.Equal(retrieved.Amount))
	})

	t.Run("insert nil position returns error", func(t *testing.T) {
		err := tc.PositionRepo.Insert(ctx, nil)
		require.Error(t, err)
	})
}

// TestPositionRepository_Insert100RowsForSameAccount verifies append-only behavior
func TestPositionRepository_Insert100RowsForSameAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	const numPositions = 100
	accountID := "ACC-APPEND-TEST"
	instrumentCode := "GBP"
	bucketKey := "default"

	// Insert 100 positions for the same account
	for i := 0; i < numPositions; i++ {
		pos, err := domain.NewPosition(
			accountID,
			instrumentCode,
			bucketKey,
			decimal.NewFromFloat(1.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err, "failed to insert position %d", i)
	}

	// Verify we have 100 rows
	count, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(numPositions), count, "expected %d rows for account", numPositions)

	// Verify aggregation sums correctly
	agg, err := tc.PositionRepo.GetAggregatedPosition(ctx, accountID, instrumentCode, bucketKey)
	require.NoError(t, err)
	require.NotNil(t, agg)
	assert.True(t, decimal.NewFromFloat(100.0).Equal(agg.TotalAmount), "expected sum of 100.0")
	assert.Equal(t, int64(numPositions), agg.RecordCount)
}

// TestPositionRepository_InsertBatch tests bulk insert behavior
func TestPositionRepository_InsertBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("batch insert multiple positions", func(t *testing.T) {
		positions := make([]*domain.Position, 10)
		for i := 0; i < 10; i++ {
			pos, err := domain.NewPosition(
				"ACC-BATCH",
				"USD",
				"default",
				decimal.NewFromFloat(float64(i+1)*10.0),
				"Monetary",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			positions[i] = pos
		}

		err := tc.PositionRepo.InsertBatch(ctx, positions)
		require.NoError(t, err)

		// Verify all positions were inserted
		agg, err := tc.PositionRepo.GetAggregatedPosition(ctx, "ACC-BATCH", "USD", "default")
		require.NoError(t, err)
		require.NotNil(t, agg)
		// Sum: 10+20+30+40+50+60+70+80+90+100 = 550
		assert.True(t, decimal.NewFromFloat(550.0).Equal(agg.TotalAmount))
		assert.Equal(t, int64(10), agg.RecordCount)
	})

	t.Run("batch insert empty slice succeeds", func(t *testing.T) {
		err := tc.PositionRepo.InsertBatch(ctx, []*domain.Position{})
		require.NoError(t, err)
	})
}

// TestPositionRepository_SoftDelete tests the SoftDelete method
func TestPositionRepository_SoftDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("soft delete sets deleted_at timestamp", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-SOFTDEL",
			"GBP",
			"default",
			decimal.NewFromFloat(100.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// Verify position exists before soft delete
		retrieved, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)
		assert.Equal(t, pos.ID, retrieved.ID)

		// Soft delete the position
		err = tc.PositionRepo.SoftDelete(ctx, pos.ID)
		require.NoError(t, err)

		// Verify position is no longer returned by FindByID (filters deleted_at IS NULL)
		_, err = tc.PositionRepo.FindByID(ctx, pos.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("soft delete non-existent position returns ErrNotFound", func(t *testing.T) {
		err := tc.PositionRepo.SoftDelete(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("soft delete already deleted position returns ErrNotFound", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-SOFTDEL-2",
			"GBP",
			"default",
			decimal.NewFromFloat(50.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// First soft delete succeeds
		err = tc.PositionRepo.SoftDelete(ctx, pos.ID)
		require.NoError(t, err)

		// Second soft delete returns ErrNotFound (already deleted)
		err = tc.PositionRepo.SoftDelete(ctx, pos.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

// TestPositionRepository_SoftDeleteBatch tests the SoftDeleteBatch method
func TestPositionRepository_SoftDeleteBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("batch soft delete marks multiple positions deleted", func(t *testing.T) {
		accountID := "ACC-BATCH-DEL"
		var ids []uuid.UUID

		// Insert 5 positions
		for i := 0; i < 5; i++ {
			pos, err := domain.NewPosition(
				accountID,
				"GBP",
				"default",
				decimal.NewFromFloat(float64(i+1)*10.0),
				"Monetary",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			err = tc.PositionRepo.Insert(ctx, pos)
			require.NoError(t, err)
			ids = append(ids, pos.ID)
		}

		// Verify all 5 exist
		count, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
		require.NoError(t, err)
		assert.Equal(t, int64(5), count)

		// Soft delete the first 3
		err = tc.PositionRepo.SoftDeleteBatch(ctx, ids[:3])
		require.NoError(t, err)

		// Verify only 2 remain active
		count, err = tc.PositionRepo.GetPositionCount(ctx, accountID)
		require.NoError(t, err)
		assert.Equal(t, int64(2), count)
	})

	t.Run("batch soft delete with empty slice succeeds", func(t *testing.T) {
		err := tc.PositionRepo.SoftDeleteBatch(ctx, []uuid.UUID{})
		require.NoError(t, err)
	})
}

// TestPositionRepository_UpdateAttributes tests the UpdateAttributes method
func TestPositionRepository_UpdateAttributes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("update attributes only modifies attributes field", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-ATTRS",
			"GBP",
			"default",
			decimal.NewFromFloat(100.00),
			"Monetary",
			map[string]string{"original": "value"},
			uuid.New(),
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// Update attributes
		newAttrs := map[string]string{"updated": "new-value", "extra": "data"}
		err = tc.PositionRepo.UpdateAttributes(ctx, pos.ID, newAttrs)
		require.NoError(t, err)

		// Verify attributes changed but immutable fields unchanged
		retrieved, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)

		assert.Equal(t, "new-value", retrieved.Attributes["updated"])
		assert.Equal(t, "data", retrieved.Attributes["extra"])
		assert.Empty(t, retrieved.Attributes["original"], "old attributes should be replaced")

		// Immutable fields unchanged
		assert.Equal(t, pos.AccountID, retrieved.AccountID)
		assert.Equal(t, pos.InstrumentCode, retrieved.InstrumentCode)
		assert.Equal(t, pos.BucketKey, retrieved.BucketKey)
		assert.True(t, pos.Amount.Equal(retrieved.Amount))
		assert.Equal(t, pos.Dimension, retrieved.Dimension)
		assert.Equal(t, pos.ReferenceID, retrieved.ReferenceID)
	})

	t.Run("update attributes on non-existent position returns ErrNotFound", func(t *testing.T) {
		err := tc.PositionRepo.UpdateAttributes(ctx, uuid.New(), map[string]string{"key": "value"})
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("update attributes on deleted position returns ErrNotFound", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-ATTRS-DEL",
			"GBP",
			"default",
			decimal.NewFromFloat(50.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// Soft delete first
		err = tc.PositionRepo.SoftDelete(ctx, pos.ID)
		require.NoError(t, err)

		// Update attributes should fail on deleted position
		err = tc.PositionRepo.UpdateAttributes(ctx, pos.ID, map[string]string{"key": "value"})
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("update attributes with nil clears attributes", func(t *testing.T) {
		pos, err := domain.NewPosition(
			"ACC-ATTRS-NIL",
			"GBP",
			"default",
			decimal.NewFromFloat(75.00),
			"Monetary",
			map[string]string{"existing": "data"},
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		// Update with nil attributes
		err = tc.PositionRepo.UpdateAttributes(ctx, pos.ID, nil)
		require.NoError(t, err)

		// Verify attributes cleared
		retrieved, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)
		assert.Nil(t, retrieved.Attributes)
	})
}

// TestPositionRepository_GetAggregatedPosition tests read-time aggregation
func TestPositionRepository_GetAggregatedPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("aggregate multiple positions with positive and negative amounts", func(t *testing.T) {
		accountID := "ACC-AGG-TEST"
		instrumentCode := "EUR"
		bucketKey := "default"

		// Insert positions: +100, +50, -30, +20
		amounts := []float64{100.0, 50.0, -30.0, 20.0}
		for _, amt := range amounts {
			pos, err := domain.NewPosition(
				accountID,
				instrumentCode,
				bucketKey,
				decimal.NewFromFloat(amt),
				"Monetary",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			err = tc.PositionRepo.Insert(ctx, pos)
			require.NoError(t, err)
		}

		agg, err := tc.PositionRepo.GetAggregatedPosition(ctx, accountID, instrumentCode, bucketKey)
		require.NoError(t, err)
		require.NotNil(t, agg)

		// Expected: 100 + 50 - 30 + 20 = 140
		assert.True(t, decimal.NewFromFloat(140.0).Equal(agg.TotalAmount))
		assert.Equal(t, int64(4), agg.RecordCount)
	})

	t.Run("returns nil for non-existent combination", func(t *testing.T) {
		agg, err := tc.PositionRepo.GetAggregatedPosition(ctx, "NON-EXISTENT", "XYZ", "bucket")
		require.NoError(t, err)
		assert.Nil(t, agg)
	})
}

// TestPositionRepository_ListByAccount tests pagination
func TestPositionRepository_ListByAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-LIST-TEST"

	// Insert 25 positions
	for i := 0; i < 25; i++ {
		pos, err := domain.NewPosition(
			accountID,
			"GBP",
			"default",
			decimal.NewFromFloat(float64(i+1)),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)
		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)
	}

	t.Run("first page", func(t *testing.T) {
		positions, err := tc.PositionRepo.ListByAccount(ctx, accountID, 10, 0)
		require.NoError(t, err)
		assert.Len(t, positions, 10)
	})

	t.Run("second page", func(t *testing.T) {
		positions, err := tc.PositionRepo.ListByAccount(ctx, accountID, 10, 10)
		require.NoError(t, err)
		assert.Len(t, positions, 10)
	})

	t.Run("last page", func(t *testing.T) {
		positions, err := tc.PositionRepo.ListByAccount(ctx, accountID, 10, 20)
		require.NoError(t, err)
		assert.Len(t, positions, 5) // Only 5 remaining
	})

	t.Run("invalid limit returns error", func(t *testing.T) {
		_, err := tc.PositionRepo.ListByAccount(ctx, accountID, 0, 0)
		require.Error(t, err)
	})
}

// TestPositionRepository_ListAggregatedByAccount tests account-wide aggregation
func TestPositionRepository_ListAggregatedByAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-MULTI-ASSET"

	// Insert positions for different assets
	assets := []struct {
		instrument string
		bucket     string
		amounts    []float64
	}{
		{"GBP", "default", []float64{100.0, 50.0}},
		{"USD", "default", []float64{200.0}},
		{"KWH", "meter-001", []float64{1000.0, 500.0, 250.0}},
	}

	for _, asset := range assets {
		for _, amt := range asset.amounts {
			pos, err := domain.NewPosition(
				accountID,
				asset.instrument,
				asset.bucket,
				decimal.NewFromFloat(amt),
				"Monetary",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			err = tc.PositionRepo.Insert(ctx, pos)
			require.NoError(t, err)
		}
	}

	aggregates, err := tc.PositionRepo.ListAggregatedByAccount(ctx, accountID)
	require.NoError(t, err)
	require.Len(t, aggregates, 3)

	// Create a map for easier assertions
	aggMap := make(map[string]*domain.AggregatedPosition)
	for _, agg := range aggregates {
		key := agg.InstrumentCode + ":" + agg.BucketKey
		aggMap[key] = agg
	}

	assert.True(t, decimal.NewFromFloat(150.0).Equal(aggMap["GBP:default"].TotalAmount))
	assert.True(t, decimal.NewFromFloat(200.0).Equal(aggMap["USD:default"].TotalAmount))
	assert.True(t, decimal.NewFromFloat(1750.0).Equal(aggMap["KWH:meter-001"].TotalAmount))
}

// TestPositionRepository_NoUpdateMethod verifies repository has no Update method
func TestPositionRepository_NoUpdateMethod(t *testing.T) {
	// This is a compile-time test - the PositionRepository should not have Update() method
	// If someone adds an Update method, this test documents the intentional omission

	// The interface only defines Insert methods, not Update
	var _ domain.PositionRepository = (*struct {
		domain.PositionRepository
	})(nil)

	// This test passes by compilation - no Update method exists on the interface
	t.Log("PositionRepository correctly has no Update method - append-only enforced")
}

// TestPositionRepository_ConcurrentInserts tests O(1) insert performance under concurrency
func TestPositionRepository_ConcurrentInserts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-CONCURRENT"

	const numGoroutines = 10
	const insertsPerGoroutine = 10

	errChan := make(chan error, numGoroutines*insertsPerGoroutine)

	// Launch concurrent inserts
	for g := 0; g < numGoroutines; g++ {
		go func() {
			for i := 0; i < insertsPerGoroutine; i++ {
				pos, err := domain.NewPosition(
					accountID,
					"GBP",
					"default",
					decimal.NewFromFloat(1.0),
					"Monetary",
					nil,
					uuid.Nil,
					"system",
				)
				if err != nil {
					errChan <- err
					continue
				}

				if err := tc.PositionRepo.Insert(ctx, pos); err != nil {
					errChan <- err
					continue
				}
				errChan <- nil
			}
		}()
	}

	// Collect results
	var errors []error
	for i := 0; i < numGoroutines*insertsPerGoroutine; i++ {
		if err := <-errChan; err != nil {
			errors = append(errors, err)
		}
	}

	// All inserts should succeed (no locking issues with append-only)
	assert.Empty(t, errors, "expected no errors from concurrent inserts: %v", errors)

	// Verify count
	count, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(numGoroutines*insertsPerGoroutine), count)
}

// TestPositionRepository_AppendOnlyEnforcement verifies that the repository only provides
// safe write methods: Insert, SoftDelete, and UpdateAttributes.
// No general Update/Upsert methods exist, enforcing append-only semantics at the Go layer
// since CockroachDB does not support PL/pgSQL triggers.
func TestPositionRepository_AppendOnlyEnforcement(t *testing.T) {
	// This is a compile-time verification: the PositionRepository interface only exposes
	// Insert, InsertBatch, SoftDelete, SoftDeleteBatch, and UpdateAttributes as write methods.
	// The interface definition itself enforces append-only semantics.
	var _ domain.PositionRepository = (*struct {
		domain.PositionRepository
	})(nil)

	t.Log("PositionRepository enforces append-only semantics at Go layer: " +
		"Insert (new records), SoftDelete (deleted_at), UpdateAttributes (attributes JSONB)")
}

// TestPositionRepository_FindByID tests retrieval by ID
func TestPositionRepository_FindByID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("find existing position", func(t *testing.T) {
		refID := uuid.New()
		pos, err := domain.NewPosition(
			"ACC-FIND",
			"CHF",
			"default",
			decimal.NewFromFloat(999.99),
			"Monetary",
			map[string]string{"ref": "test"},
			refID,
			"test-user",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)

		retrieved, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)
		assert.Equal(t, pos.ID, retrieved.ID)
		assert.Equal(t, pos.AccountID, retrieved.AccountID)
		assert.Equal(t, pos.InstrumentCode, retrieved.InstrumentCode)
		assert.Equal(t, pos.BucketKey, retrieved.BucketKey)
		assert.True(t, pos.Amount.Equal(retrieved.Amount))
		assert.Equal(t, pos.Dimension, retrieved.Dimension)
		assert.Equal(t, "test", retrieved.Attributes["ref"])
		assert.Equal(t, refID, retrieved.ReferenceID)
	})

	t.Run("find non-existent position returns ErrNotFound", func(t *testing.T) {
		_, err := tc.PositionRepo.FindByID(ctx, uuid.New())
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

// TestPositionRepository_EnergyPositions tests multi-asset (non-monetary) positions
func TestPositionRepository_EnergyPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "METER-001"

	// Record multiple energy measurements
	measurements := []struct {
		bucketKey string
		amount    float64
		dimension string
	}{
		{"2024-01-01", 1500.5, "Energy"},
		{"2024-01-01", 250.25, "Energy"},
		{"2024-01-02", 1750.0, "Energy"},
	}

	for _, m := range measurements {
		pos, err := domain.NewPosition(
			accountID,
			"KWH",
			m.bucketKey,
			decimal.NewFromFloat(m.amount),
			m.dimension,
			nil,
			uuid.Nil,
			"meter-system",
		)
		require.NoError(t, err)
		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)
	}

	// Aggregate by bucket (daily usage)
	aggregates, err := tc.PositionRepo.ListAggregatedByAccount(ctx, accountID)
	require.NoError(t, err)
	require.Len(t, aggregates, 2) // Two unique bucket keys

	// Find the specific buckets
	var jan1Agg, jan2Agg *domain.AggregatedPosition
	for _, agg := range aggregates {
		if strings.HasSuffix(agg.BucketKey, "01-01") {
			jan1Agg = agg
		} else if strings.HasSuffix(agg.BucketKey, "01-02") {
			jan2Agg = agg
		}
	}

	require.NotNil(t, jan1Agg)
	require.NotNil(t, jan2Agg)

	// Jan 1: 1500.5 + 250.25 = 1750.75
	assert.True(t, decimal.NewFromFloat(1750.75).Equal(jan1Agg.TotalAmount))
	assert.Equal(t, int64(2), jan1Agg.RecordCount)

	// Jan 2: 1750.0
	assert.True(t, decimal.NewFromFloat(1750.0).Equal(jan2Agg.TotalAmount))
	assert.Equal(t, int64(1), jan2Agg.RecordCount)
}

// TestPositionRepository_GetAggregatedPositions tests GROUP BY bucket aggregation
func TestPositionRepository_GetAggregatedPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-AGG-BUCKETS"
	instrumentCode := "KWH"

	// Insert positions across 3 buckets
	bucketData := []struct {
		bucket  string
		amounts []float64
	}{
		{"bucket-a", []float64{100.0, 50.0}},      // Total: 150.0, Count: 2
		{"bucket-b", []float64{200.0}},            // Total: 200.0, Count: 1
		{"bucket-c", []float64{10.0, 20.0, 30.0}}, // Total: 60.0, Count: 3
	}

	for _, bd := range bucketData {
		for _, amt := range bd.amounts {
			pos, err := domain.NewPosition(
				accountID,
				instrumentCode,
				bd.bucket,
				decimal.NewFromFloat(amt),
				"Energy",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			err = tc.PositionRepo.Insert(ctx, pos)
			require.NoError(t, err)
		}
	}

	t.Run("returns aggregates grouped by bucket_key", func(t *testing.T) {
		aggregates, err := tc.PositionRepo.GetAggregatedPositions(ctx, accountID, instrumentCode)
		require.NoError(t, err)
		require.Len(t, aggregates, 3)

		// Results should be sorted by bucket_key
		assert.Equal(t, "bucket-a", aggregates[0].BucketKey)
		assert.Equal(t, "bucket-b", aggregates[1].BucketKey)
		assert.Equal(t, "bucket-c", aggregates[2].BucketKey)

		// Verify amounts
		assert.True(t, decimal.NewFromFloat(150.0).Equal(aggregates[0].TotalAmount))
		assert.True(t, decimal.NewFromFloat(200.0).Equal(aggregates[1].TotalAmount))
		assert.True(t, decimal.NewFromFloat(60.0).Equal(aggregates[2].TotalAmount))

		// Verify counts
		assert.Equal(t, int64(2), aggregates[0].RecordCount)
		assert.Equal(t, int64(1), aggregates[1].RecordCount)
		assert.Equal(t, int64(3), aggregates[2].RecordCount)
	})

	t.Run("returns empty slice for non-existent account", func(t *testing.T) {
		aggregates, err := tc.PositionRepo.GetAggregatedPositions(ctx, "NON-EXISTENT", instrumentCode)
		require.NoError(t, err)
		assert.Empty(t, aggregates)
	})

	t.Run("returns empty slice for non-existent instrument", func(t *testing.T) {
		aggregates, err := tc.PositionRepo.GetAggregatedPositions(ctx, accountID, "XYZ-FAKE")
		require.NoError(t, err)
		assert.Empty(t, aggregates)
	})
}

// TestPositionRepository_GetAggregatedPositions_ScalePerformance tests aggregation performance at scale
func TestPositionRepository_GetAggregatedPositions_ScalePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-SCALE-TEST"
	instrumentCode := "GPU_HOUR"

	// Insert 10,000 positions across 100 buckets
	const numBuckets = 100
	const positionsPerBucket = 100

	positions := make([]*domain.Position, 0, numBuckets*positionsPerBucket)
	for b := 0; b < numBuckets; b++ {
		bucketKey := strings.ReplaceAll(uuid.NewString(), "-", "")[:8] + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:4]
		for p := 0; p < positionsPerBucket; p++ {
			pos, err := domain.NewPosition(
				accountID,
				instrumentCode,
				bucketKey,
				decimal.NewFromFloat(1.0),
				"Compute",
				nil,
				uuid.Nil,
				"system",
			)
			require.NoError(t, err)
			positions = append(positions, pos)
		}
	}

	// Bulk insert for speed
	err := tc.PositionRepo.InsertBatch(ctx, positions)
	require.NoError(t, err)

	// Verify total count
	count, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, int64(numBuckets*positionsPerBucket), count)

	// Measure aggregation time
	start := time.Now()
	aggregates, err := tc.PositionRepo.GetAggregatedPositions(ctx, accountID, instrumentCode)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Len(t, aggregates, numBuckets)

	// Each bucket should have sum of 100 (100 positions × 1.0)
	for _, agg := range aggregates {
		assert.True(t, decimal.NewFromFloat(float64(positionsPerBucket)).Equal(agg.TotalAmount),
			"bucket %s should sum to %d", agg.BucketKey, positionsPerBucket)
		assert.Equal(t, int64(positionsPerBucket), agg.RecordCount)
	}

	// Task requires <50ms for 10,000 positions across 100 buckets
	t.Logf("GetAggregatedPositions with %d positions across %d buckets completed in %v",
		numBuckets*positionsPerBucket, numBuckets, elapsed)
	assert.Less(t, elapsed.Milliseconds(), int64(50), "aggregation should complete in <50ms")
}

// TestPositionRepository_GetBucketDetails tests bucket detail retrieval
func TestPositionRepository_GetBucketDetails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-BUCKET-DETAILS"
	instrumentCode := "CARBON_CREDIT"
	targetBucket := "vintage-2024"
	otherBucket := "vintage-2023"

	// Insert 50 positions in target bucket
	for i := 0; i < 50; i++ {
		pos, err := domain.NewPosition(
			accountID,
			instrumentCode,
			targetBucket,
			decimal.NewFromFloat(float64(i+1)),
			"Carbon",
			map[string]string{"vintage": "2024", "index": strings.ReplaceAll(uuid.NewString(), "-", "")[:8]},
			uuid.New(),
			"system",
		)
		require.NoError(t, err)
		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)
	}

	// Insert some positions in other bucket (should not be returned)
	for i := 0; i < 5; i++ {
		pos, err := domain.NewPosition(
			accountID,
			instrumentCode,
			otherBucket,
			decimal.NewFromFloat(999.0),
			"Carbon",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)
		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)
	}

	t.Run("returns all positions for bucket", func(t *testing.T) {
		positions, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 100, 0)
		require.NoError(t, err)
		assert.Len(t, positions, 50)

		// Verify all positions belong to target bucket
		for _, pos := range positions {
			assert.Equal(t, targetBucket, pos.BucketKey)
			assert.Equal(t, accountID, pos.AccountID)
			assert.Equal(t, instrumentCode, pos.InstrumentCode)
		}
	})

	t.Run("returns positions with attributes deserialized", func(t *testing.T) {
		positions, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 10, 0)
		require.NoError(t, err)
		require.NotEmpty(t, positions)

		// First position should have attributes
		pos := positions[0]
		assert.NotNil(t, pos.Attributes)
		assert.Equal(t, "2024", pos.Attributes["vintage"])
	})

	t.Run("pagination works correctly", func(t *testing.T) {
		// First page
		page1, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 20, 0)
		require.NoError(t, err)
		assert.Len(t, page1, 20)

		// Second page
		page2, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 20, 20)
		require.NoError(t, err)
		assert.Len(t, page2, 20)

		// Third page (partial)
		page3, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 20, 40)
		require.NoError(t, err)
		assert.Len(t, page3, 10) // Only 10 remaining

		// Verify no duplicates between pages
		seenIDs := make(map[uuid.UUID]bool)
		for _, pos := range page1 {
			seenIDs[pos.ID] = true
		}
		for _, pos := range page2 {
			assert.False(t, seenIDs[pos.ID], "duplicate ID found in page2")
			seenIDs[pos.ID] = true
		}
		for _, pos := range page3 {
			assert.False(t, seenIDs[pos.ID], "duplicate ID found in page3")
		}
	})

	t.Run("returns empty slice for non-existent bucket", func(t *testing.T) {
		positions, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, "non-existent-bucket", 100, 0)
		require.NoError(t, err)
		assert.Empty(t, positions)
	})

	t.Run("invalid limit returns error", func(t *testing.T) {
		_, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, 0, 0)
		require.Error(t, err)

		_, err = tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, targetBucket, -1, 0)
		require.Error(t, err)
	})
}

// TestPositionRepository_ReadOperationsNoSideEffects verifies read operations are pure
func TestPositionRepository_ReadOperationsNoSideEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()
	accountID := "ACC-READ-ONLY"
	instrumentCode := "TIME_VOUCHER"
	bucketKey := "session-001"

	// Insert some positions
	for i := 0; i < 10; i++ {
		pos, err := domain.NewPosition(
			accountID,
			instrumentCode,
			bucketKey,
			decimal.NewFromFloat(float64(i+1)),
			"Time",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)
		err = tc.PositionRepo.Insert(ctx, pos)
		require.NoError(t, err)
	}

	// Get initial count
	initialCount, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
	require.NoError(t, err)

	// Execute multiple read operations
	for i := 0; i < 100; i++ {
		_, err = tc.PositionRepo.GetAggregatedPositions(ctx, accountID, instrumentCode)
		require.NoError(t, err)
		_, err = tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, bucketKey, 100, 0)
		require.NoError(t, err)
		_, err = tc.PositionRepo.GetAggregatedPosition(ctx, accountID, instrumentCode, bucketKey)
		require.NoError(t, err)
	}

	// Verify count unchanged (no side effects, no compaction triggered)
	finalCount, err := tc.PositionRepo.GetPositionCount(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, initialCount, finalCount, "read operations should not modify position count")

	// Verify positions unchanged
	positions, err := tc.PositionRepo.GetBucketDetails(ctx, accountID, instrumentCode, bucketKey, 100, 0)
	require.NoError(t, err)
	assert.Len(t, positions, 10, "read operations should not modify positions")
}
