package engine

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestNewStreamConfig(t *testing.T) {
	t.Parallel()

	config := NewStreamConfig("KWH")

	assert.Equal(t, DefaultBatchSize, config.BatchSize)
	assert.Equal(t, "KWH", config.InstrumentCode)
	assert.Empty(t, config.OldBucketIDFilter)
}

func TestStreamConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := StreamConfig{}

	// Verify zero values
	assert.Equal(t, 0, config.BatchSize)
	assert.Empty(t, config.InstrumentCode)
	assert.Empty(t, config.OldBucketIDFilter)
}

func TestDefaultBatchSize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 1000, DefaultBatchSize)
}

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	// Verify error messages are descriptive
	assert.Contains(t, ErrInvalidCELExpression.Error(), "CEL")
	assert.Contains(t, ErrCELEvaluation.Error(), "CEL")
	assert.Contains(t, ErrMeasurementStream.Error(), "stream")
	assert.Contains(t, ErrInstrumentNotFound.Error(), "instrument")
	assert.Contains(t, ErrInstrumentMismatch.Error(), "mismatch")
	assert.Contains(t, ErrNoMeasurementsFound.Error(), "measurement")
}

func TestRebucketingPlan_ZeroValue(t *testing.T) {
	t.Parallel()

	plan := RebucketingPlan{}

	assert.Empty(t, plan.InstrumentCode)
	assert.Equal(t, 0, plan.OldInstrumentVersion)
	assert.Equal(t, 0, plan.NewInstrumentVersion)
	assert.Nil(t, plan.BucketMappings)
	assert.Nil(t, plan.AffectedPositionIDs)
	assert.Equal(t, int64(0), plan.ProcessedCount)
	assert.Equal(t, int64(0), plan.TotalCount)
	assert.Equal(t, int64(0), plan.ErrorCount)
	assert.Equal(t, int64(0), plan.SkippedCount)
}

func TestBucketMapping_ZeroValue(t *testing.T) {
	t.Parallel()

	mapping := BucketMapping{}

	assert.Empty(t, mapping.OldBucketID)
	assert.Empty(t, mapping.NewBucketID)
	assert.Nil(t, mapping.MeasurementIDs)
	assert.Nil(t, mapping.PositionIDs)
	assert.Equal(t, int64(0), mapping.MeasurementCount)
}

func TestProgress_ZeroValue(t *testing.T) {
	t.Parallel()

	progress := Progress{}

	assert.Equal(t, int64(0), progress.Processed)
	assert.Equal(t, int64(0), progress.Total)
	assert.Equal(t, 0, progress.CurrentBatch)
	assert.Equal(t, 0, progress.TotalBatches)
	assert.Equal(t, float64(0), progress.Rate)
}

func TestMeasurementRecord_ZeroValue(t *testing.T) {
	t.Parallel()

	record := MeasurementRecord{}

	assert.Equal(t, uuid.Nil, record.ID)
	assert.Equal(t, uuid.Nil, record.FinancialPositionLogID)
	assert.Empty(t, record.CurrentBucketID)
	assert.Nil(t, record.Metadata)
}

func TestRebucketingResult_ZeroValue(t *testing.T) {
	t.Parallel()

	result := RebucketingResult{}

	assert.Equal(t, uuid.Nil, result.MeasurementID)
	assert.Equal(t, uuid.Nil, result.FinancialPositionLogID)
	assert.Empty(t, result.OldBucketID)
	assert.Empty(t, result.NewBucketID)
	assert.False(t, result.Changed)
	assert.Nil(t, result.Error)
}
