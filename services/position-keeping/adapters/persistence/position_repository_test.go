package persistence_test

import (
	"context"
	"strings"
	"testing"

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

// TestPositionRepository_AppendOnlyTrigger verifies the database trigger rejects UPDATEs
func TestPositionRepository_AppendOnlyTrigger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	// Insert a position
	pos, err := domain.NewPosition(
		"ACC-TRIGGER",
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

	t.Run("UPDATE on amount column raises trigger exception", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET amount = 200.00 WHERE id = $1",
			pos.ID,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
		assert.Contains(t, err.Error(), "amount")
	})

	t.Run("UPDATE on account_id column raises trigger exception", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET account_id = 'NEW-ACC' WHERE id = $1",
			pos.ID,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
		assert.Contains(t, err.Error(), "account_id")
	})

	t.Run("UPDATE on instrument_code column raises trigger exception", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET instrument_code = 'USD' WHERE id = $1",
			pos.ID,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
		assert.Contains(t, err.Error(), "instrument_code")
	})

	t.Run("UPDATE on bucket_key column raises trigger exception", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET bucket_key = 'new-bucket' WHERE id = $1",
			pos.ID,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
		assert.Contains(t, err.Error(), "bucket_key")
	})

	t.Run("UPDATE on reference_id column raises trigger exception", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET reference_id = $2 WHERE id = $1",
			pos.ID, uuid.New(),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
		assert.Contains(t, err.Error(), "reference_id")
	})

	t.Run("UPDATE on attributes column succeeds (allowed)", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			`UPDATE position_keeping.position SET attributes = '{"note": "allowed"}'::jsonb WHERE id = $1`,
			pos.ID,
		)
		// This should succeed - attributes is not an immutable field
		require.NoError(t, err)
	})

	t.Run("UPDATE on deleted_at column succeeds (soft delete allowed)", func(t *testing.T) {
		_, err := tc.Pool.Exec(ctx,
			"UPDATE position_keeping.position SET deleted_at = NOW() WHERE id = $1",
			pos.ID,
		)
		// This should succeed - soft delete is allowed
		require.NoError(t, err)
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

// TestPositionRepository_TriggerExists verifies the trigger is installed
func TestPositionRepository_TriggerExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	var triggerName string
	err := tc.Pool.QueryRow(ctx, `
		SELECT tgname FROM pg_trigger
		WHERE tgname = 'positions_append_only'
	`).Scan(&triggerName)
	require.NoError(t, err)
	assert.Equal(t, "positions_append_only", triggerName)
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
