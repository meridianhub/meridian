package worker

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsolidatePositions_SinglePosition(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC()

	positions := []PositionRow{
		{
			ID:        id,
			Amount:    decimal.NewFromFloat(100.50),
			Dimension: "Monetary",
			Attributes: map[string]string{
				"key": "value",
			},
			ReferenceID: uuid.New(),
			CreatedAt:   now,
		},
	}

	result := consolidatePositions(positions)

	assert.True(t, decimal.NewFromFloat(100.50).Equal(result.Amount))
	assert.Equal(t, "Monetary", result.Dimension)
	assert.Equal(t, "value", result.Attributes["key"])
	assert.Len(t, result.OriginalIDs, 1)
	assert.Equal(t, id, result.OriginalIDs[0])
}

func TestConsolidatePositions_MultiplePositions(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-1 * time.Hour)

	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	positions := []PositionRow{
		{
			ID:        id1,
			Amount:    decimal.NewFromFloat(100.00),
			Dimension: "Monetary",
			Attributes: map[string]string{
				"old_key": "old_value",
			},
			CreatedAt: earlier,
		},
		{
			ID:        id2,
			Amount:    decimal.NewFromFloat(50.25),
			Dimension: "Energy",
			Attributes: map[string]string{
				"new_key": "new_value",
			},
			CreatedAt: now,
		},
		{
			ID:        id3,
			Amount:    decimal.NewFromFloat(-25.75),
			Dimension: "Monetary",
			CreatedAt: earlier.Add(-1 * time.Hour),
		},
	}

	result := consolidatePositions(positions)

	// Amount should be sum: 100.00 + 50.25 + (-25.75) = 124.50
	assert.True(t, decimal.NewFromFloat(124.50).Equal(result.Amount))
	// Dimension and attributes from the most recent position (id2)
	assert.Equal(t, "Energy", result.Dimension)
	assert.Equal(t, "new_value", result.Attributes["new_key"])
	assert.Len(t, result.OriginalIDs, 3)
}

func TestConsolidatePositions_NilAttributes(t *testing.T) {
	now := time.Now().UTC()

	positions := []PositionRow{
		{
			ID:         uuid.New(),
			Amount:     decimal.NewFromFloat(50.00),
			Dimension:  "Monetary",
			Attributes: nil,
			CreatedAt:  now,
		},
	}

	result := consolidatePositions(positions)

	assert.True(t, decimal.NewFromFloat(50.00).Equal(result.Amount))
	assert.Nil(t, result.Attributes)
}

func TestConsolidatePositions_ZeroAmounts(t *testing.T) {
	now := time.Now().UTC()

	positions := []PositionRow{
		{
			ID:        uuid.New(),
			Amount:    decimal.NewFromFloat(100.00),
			Dimension: "Monetary",
			CreatedAt: now,
		},
		{
			ID:        uuid.New(),
			Amount:    decimal.NewFromFloat(-100.00),
			Dimension: "Monetary",
			CreatedAt: now.Add(-1 * time.Minute),
		},
	}

	result := consolidatePositions(positions)

	assert.True(t, decimal.Zero.Equal(result.Amount))
}

func TestLogIterationComplete_NoProcessedNoBuckets(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w := &CompactionWorker{
		pool:   pool,
		logger: logger.With("component", "compaction_worker"),
		config: validConfig(),
	}

	// Should not panic with zero processed and no errors
	w.logIterationComplete(time.Now(), 0, 0, nil)
}

func TestLogIterationComplete_WithErrors(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w := &CompactionWorker{
		pool:   pool,
		logger: logger.With("component", "compaction_worker"),
		config: validConfig(),
	}

	errs := []error{
		assert.AnError,
	}

	// Should not panic with errors
	w.logIterationComplete(time.Now(), 2, 10, errs)
}

func TestLogIterationComplete_SuccessWithBuckets(t *testing.T) {
	pool := mockPool(t)
	defer pool.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	w := &CompactionWorker{
		pool:   pool,
		logger: logger.With("component", "compaction_worker"),
		config: validConfig(),
	}

	// Should not panic with successful processing
	w.logIterationComplete(time.Now(), 5, 25, nil)
}

func TestMetrics_RecordBucketCompacted(t *testing.T) {
	// Should not panic
	RecordBucketCompacted()
}

func TestMetrics_RecordRowsConsolidated(t *testing.T) {
	// Should not panic
	RecordRowsConsolidated(10)
	RecordRowsConsolidated(0)
}

func TestMetrics_SetFragmentedBucketsCount(t *testing.T) {
	// Should not panic
	SetFragmentedBucketsCount(5)
	SetFragmentedBucketsCount(0)
}

func TestMetrics_RecordCompactionError_AllTypes(t *testing.T) {
	errorTypes := []string{
		ErrorTypeScan,
		ErrorTypeLock,
		ErrorTypeInsert,
		ErrorTypeDelete,
		ErrorTypeTx,
	}

	for _, errType := range errorTypes {
		t.Run(errType, func(t *testing.T) {
			// Should not panic
			RecordCompactionError(errType)
		})
	}
}

func TestMetrics_ObserveCompactionDuration(t *testing.T) {
	// Should not panic
	ObserveCompactionDuration(1.5)
	ObserveCompactionDuration(0.0)
}

func TestMetrics_RecordCompactionRun(t *testing.T) {
	// Should not panic
	RecordCompactionRun()
}

func TestMetrics_ExposeMetricsForTesting(t *testing.T) {
	// Verify the exposed metrics struct is properly initialized
	require.NotNil(t, ExposeMetricsForTesting.CompactionRunsTotal)
	require.NotNil(t, ExposeMetricsForTesting.CompactionBucketsCompactedTotal)
	require.NotNil(t, ExposeMetricsForTesting.CompactionRowsConsolidatedTotal)
	require.NotNil(t, ExposeMetricsForTesting.CompactionErrorsTotal)
	require.NotNil(t, ExposeMetricsForTesting.CompactionDurationSeconds)
	require.NotNil(t, ExposeMetricsForTesting.FragmentedBucketsGauge)
}

func TestErrorTypeConstants(t *testing.T) {
	assert.Equal(t, "scan_error", ErrorTypeScan)
	assert.Equal(t, "lock_error", ErrorTypeLock)
	assert.Equal(t, "insert_error", ErrorTypeInsert)
	assert.Equal(t, "delete_error", ErrorTypeDelete)
	assert.Equal(t, "tx_error", ErrorTypeTx)
}
