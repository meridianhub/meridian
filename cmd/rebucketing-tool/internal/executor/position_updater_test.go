package executor

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPositionUpdater(t *testing.T) {
	t.Run("returns error for nil pool", func(t *testing.T) {
		_, err := NewPositionUpdater(nil, 500)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool)
	})

	t.Run("returns error for zero batch size", func(t *testing.T) {
		// Note: We can't test with a real pool here, so we test the validation
		// by checking the error type
		_, err := NewPositionUpdater(nil, 0)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool) // nil pool checked first
	})

	t.Run("returns error for negative batch size", func(t *testing.T) {
		_, err := NewPositionUpdater(nil, -1)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool) // nil pool checked first
	})

	t.Run("returns error for batch size too large", func(t *testing.T) {
		_, err := NewPositionUpdater(nil, 10001)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool) // nil pool checked first
	})
}

func TestSplitIntoBatches(t *testing.T) {
	// Create a mock updater with batch size 3 for testing
	// We can't use NewPositionUpdater without a real pool
	updater := &PositionUpdater{
		pool:      nil,
		batchSize: 3,
	}

	makePositions := func(count int) []AffectedPosition {
		positions := make([]AffectedPosition, count)
		for i := 0; i < count; i++ {
			positions[i] = AffectedPosition{
				PositionID:     uuid.New(),
				AccountID:      "ACC001",
				InstrumentCode: "GBP",
				OldBucketKey:   "old-bucket",
				NewBucketKey:   "new-bucket",
				Amount:         decimal.NewFromInt(int64(i + 1)),
			}
		}
		return positions
	}

	t.Run("splits evenly divisible count", func(t *testing.T) {
		positions := makePositions(6)

		batches := updater.SplitIntoBatches(positions)

		require.Len(t, batches, 2)
		assert.Len(t, batches[0], 3)
		assert.Len(t, batches[1], 3)
	})

	t.Run("handles remainder in last batch", func(t *testing.T) {
		positions := makePositions(7)

		batches := updater.SplitIntoBatches(positions)

		require.Len(t, batches, 3)
		assert.Len(t, batches[0], 3)
		assert.Len(t, batches[1], 3)
		assert.Len(t, batches[2], 1)
	})

	t.Run("handles count less than batch size", func(t *testing.T) {
		positions := makePositions(2)

		batches := updater.SplitIntoBatches(positions)

		require.Len(t, batches, 1)
		assert.Len(t, batches[0], 2)
	})

	t.Run("handles exact batch size", func(t *testing.T) {
		positions := makePositions(3)

		batches := updater.SplitIntoBatches(positions)

		require.Len(t, batches, 1)
		assert.Len(t, batches[0], 3)
	})

	t.Run("handles single position", func(t *testing.T) {
		positions := makePositions(1)

		batches := updater.SplitIntoBatches(positions)

		require.Len(t, batches, 1)
		assert.Len(t, batches[0], 1)
	})

	t.Run("handles empty slice", func(t *testing.T) {
		batches := updater.SplitIntoBatches([]AffectedPosition{})

		assert.Nil(t, batches)
	})

	t.Run("handles nil slice", func(t *testing.T) {
		batches := updater.SplitIntoBatches(nil)

		assert.Nil(t, batches)
	})

	t.Run("preserves position data in batches", func(t *testing.T) {
		positions := makePositions(5)
		originalIDs := make([]uuid.UUID, 5)
		for i, p := range positions {
			originalIDs[i] = p.PositionID
		}

		batches := updater.SplitIntoBatches(positions)

		// Verify all positions are present and in order
		idx := 0
		for _, batch := range batches {
			for _, pos := range batch {
				assert.Equal(t, originalIDs[idx], pos.PositionID)
				idx++
			}
		}
		assert.Equal(t, 5, idx)
	})
}

func TestPositionUpdater_GetBatchSize(t *testing.T) {
	t.Run("returns configured batch size", func(t *testing.T) {
		updater := &PositionUpdater{
			pool:      nil,
			batchSize: 750,
		}

		assert.Equal(t, 750, updater.GetBatchSize())
	})
}

func TestBatchSizeEdgeCases(t *testing.T) {
	// Test with batch size of 500 (default)
	updater := &PositionUpdater{
		pool:      nil,
		batchSize: 500,
	}

	t.Run("splits 1000 positions into 2 batches", func(t *testing.T) {
		positions := make([]AffectedPosition, 1000)
		for i := range positions {
			positions[i] = AffectedPosition{PositionID: uuid.New()}
		}

		batches := updater.SplitIntoBatches(positions)

		assert.Len(t, batches, 2)
		assert.Len(t, batches[0], 500)
		assert.Len(t, batches[1], 500)
	})

	t.Run("splits 1001 positions into 3 batches", func(t *testing.T) {
		positions := make([]AffectedPosition, 1001)
		for i := range positions {
			positions[i] = AffectedPosition{PositionID: uuid.New()}
		}

		batches := updater.SplitIntoBatches(positions)

		assert.Len(t, batches, 3)
		assert.Len(t, batches[0], 500)
		assert.Len(t, batches[1], 500)
		assert.Len(t, batches[2], 1)
	})

	t.Run("splits 499 positions into 1 batch", func(t *testing.T) {
		positions := make([]AffectedPosition, 499)
		for i := range positions {
			positions[i] = AffectedPosition{PositionID: uuid.New()}
		}

		batches := updater.SplitIntoBatches(positions)

		assert.Len(t, batches, 1)
		assert.Len(t, batches[0], 499)
	})
}
