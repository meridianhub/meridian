package infra

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBatchInserter_Defaults(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		DatasetCode: "USD_EUR_FX",
		SourceCode:  "BLOOMBERG",
	})
	require.NotNil(t, bi)

	stats := bi.Stats()
	assert.Equal(t, DefaultBatchSize, stats.BatchSize)
	assert.Equal(t, 0, stats.TotalInserted)
	assert.Equal(t, 0, stats.BatchCount)
	assert.Equal(t, 0, stats.BufferSize)
}

func TestNewBatchInserter_CustomBatchSize(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize: 100,
	})
	assert.Equal(t, 100, bi.Stats().BatchSize)
}

func TestNewBatchInserter_NegativeBatchSizeUsesDefault(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize: -1,
	})
	assert.Equal(t, DefaultBatchSize, bi.Stats().BatchSize)
}

func TestBatchInserter_AddWithNilClientReturnsError(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize: 10,
	})

	entry := &ObservationEntry{
		DatasetCode:  "USD_EUR_FX",
		Value:        "1.0856",
		QualityLevel: "ACTUAL",
	}

	err := bi.Add(context.Background(), entry)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNilClient))
}

func TestBatchInserter_FlushEmptyBufferIsNoop(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize: 10,
	})

	err := bi.Flush(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 0, bi.Stats().BatchCount)
}

func TestBatchInserter_Reset(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize: 10,
	})

	// Directly set internal state
	bi.totalInserted = 100
	bi.batchCount = 5
	bi.buffer = append(bi.buffer, &ObservationEntry{Value: "1.0"})

	bi.Reset()

	stats := bi.Stats()
	assert.Equal(t, 0, stats.TotalInserted)
	assert.Equal(t, 0, stats.BatchCount)
	assert.Equal(t, 0, stats.BufferSize)
}

func TestBatchInserter_DefaultCodesConfigured(t *testing.T) {
	// Verify that the batch inserter stores the configured codes for later use
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize:   100,
		DatasetCode: "MY_DATASET",
		SourceCode:  "MY_SOURCE",
	})

	// The inserter stores these for applying to entries when client is set
	assert.Equal(t, "MY_DATASET", bi.datasetCode)
	assert.Equal(t, "MY_SOURCE", bi.sourceCode)
}

func TestBatchInserter_AddPreservesExistingCodes(t *testing.T) {
	bi := NewBatchInserter(BatchInserterConfig{
		BatchSize:   100,
		DatasetCode: "DEFAULT_DATASET",
		SourceCode:  "DEFAULT_SOURCE",
	})

	entry := &ObservationEntry{
		DatasetCode:  "CUSTOM_DATASET",
		SourceCode:   "CUSTOM_SOURCE",
		Value:        "1.0",
		QualityLevel: "ACTUAL",
	}

	_ = bi.Add(context.Background(), entry)

	// Custom codes should be preserved
	assert.Equal(t, "CUSTOM_DATASET", entry.DatasetCode)
	assert.Equal(t, "CUSTOM_SOURCE", entry.SourceCode)
}

func TestBatchInserterErrors(t *testing.T) {
	assert.NotNil(t, ErrNilClient)
	assert.NotNil(t, ErrInvalidBatchSize)
	assert.NotNil(t, ErrBatchInsertFailed)
}
