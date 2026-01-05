package validation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

func TestPipeline_ValidateRow_AllValid(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "account-123",
		InstrumentCode: "USD",
		Amount:         "100.00",
		BucketKey:      "bucket-1",
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.False(t, rowErr.HasErrors())
}

func TestPipeline_ValidateRow_MissingRequiredFields(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber: 1,
		// All required fields missing
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.True(t, rowErr.HasErrors())
	// Should have errors for account_id, instrument_code, amount, bucket_key
	assert.GreaterOrEqual(t, len(rowErr.Errors), 4)
}

func TestPipeline_ValidateRow_DuplicateMeasurement(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// First row - should be valid
	row1 := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "acc-1",
		InstrumentCode: "USD",
		Amount:         "100",
		BucketKey:      "key-1",
	}
	rowErr1 := pipeline.ValidateRow(ctx, row1)
	assert.False(t, rowErr1.HasErrors())

	// Second row with same measurement ID - should be duplicate
	row2 := &ImportRow{
		LineNumber:     2,
		MeasurementID:  "m-1", // Duplicate
		AccountID:      "acc-2",
		InstrumentCode: "USD",
		Amount:         "200",
		BucketKey:      "key-2",
	}
	rowErr2 := pipeline.ValidateRow(ctx, row2)
	assert.True(t, rowErr2.HasErrors())

	// Check that it's a duplicate error
	hasDupeError := false
	for _, err := range rowErr2.Errors {
		var dupeErr *DuplicateError
		if errors.As(err, &dupeErr) {
			hasDupeError = true
			break
		}
	}
	assert.True(t, hasDupeError, "expected a DuplicateError")
}

func TestPipeline_ValidateRow_InstrumentNotFound(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{}, // Empty - no instruments
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "acc-1",
		InstrumentCode: "UNKNOWN",
		Amount:         "100",
		BucketKey:      "key-1",
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.True(t, rowErr.HasErrors())

	// Check for instrument not found error
	hasInstError := false
	for _, err := range rowErr.Errors {
		var fe *FieldError
		if errors.As(err, &fe) && fe.Field == "instrument_code" {
			hasInstError = true
			break
		}
	}
	assert.True(t, hasInstError, "expected instrument not found error")
}

func TestPipeline_ValidateRow_InstrumentNotActive(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"DRAFT_INST": {
				Code:   "DRAFT_INST",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT, // Not active
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "acc-1",
		InstrumentCode: "DRAFT_INST",
		Amount:         "100",
		BucketKey:      "key-1",
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.True(t, rowErr.HasErrors())
}

func TestPipeline_ValidateRow_CollectsAllErrors(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{},
	}

	// Not fail-fast - should collect all errors
	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
		FailFast:          false,
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "",        // Missing
		InstrumentCode: "UNKNOWN", // Not found
		Amount:         "",        // Missing
		BucketKey:      "",        // Missing
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.True(t, rowErr.HasErrors())
	// Should have multiple errors: missing fields + instrument not found
	assert.Greater(t, len(rowErr.Errors), 1, "expected multiple errors")
}

func TestPipeline_ValidateRow_FailFast(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
		FailFast:          true, // Stop on first error
	})
	require.NoError(t, err)

	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "",        // Missing - first error
		InstrumentCode: "UNKNOWN", // Would also fail, but fail-fast stops
		Amount:         "",        // Missing
		BucketKey:      "",        // Missing
	}

	rowErr := pipeline.ValidateRow(context.Background(), row)
	assert.True(t, rowErr.HasErrors())
	// With fail-fast, we get errors for missing fields (all checked in same layer)
	// but we stop before instrument check
}

func TestPipeline_ValidateBatch(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	rows := []ImportRow{
		{LineNumber: 1, MeasurementID: "m-1", AccountID: "acc-1", InstrumentCode: "USD", Amount: "100", BucketKey: "k1"},
		{LineNumber: 2, MeasurementID: "m-2", AccountID: "", InstrumentCode: "USD", Amount: "200", BucketKey: "k2"}, // Error
		{LineNumber: 3, MeasurementID: "m-3", AccountID: "acc-3", InstrumentCode: "USD", Amount: "300", BucketKey: "k3"},
	}

	results := pipeline.ValidateBatch(context.Background(), rows)

	// Only line 2 should have errors
	assert.Len(t, results, 1)
	assert.Contains(t, results, 2)
}

func TestPipeline_ValidateWithCallback(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	rows := []ImportRow{
		{LineNumber: 1, MeasurementID: "m-1", AccountID: "acc-1", InstrumentCode: "USD", Amount: "100", BucketKey: "k1"},
		{LineNumber: 2, MeasurementID: "m-2", AccountID: "", InstrumentCode: "USD", Amount: "200", BucketKey: "k2"}, // Error
		{LineNumber: 3, MeasurementID: "m-3", AccountID: "acc-3", InstrumentCode: "USD", Amount: "300", BucketKey: "k3"},
	}

	validCount := 0
	invalidCount := 0

	err = pipeline.ValidateWithCallback(
		context.Background(),
		rows,
		func(_ *ImportRow) error {
			validCount++
			return nil
		},
		func(_ *ImportRow, _ *RowValidationError) error {
			invalidCount++
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, 2, validCount)
	assert.Equal(t, 1, invalidCount)
}

func TestPipeline_Summary(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Validate some rows
	rows := []ImportRow{
		{LineNumber: 1, MeasurementID: "m-1", AccountID: "acc-1", InstrumentCode: "USD", Amount: "100", BucketKey: "k1"},
		{LineNumber: 2, MeasurementID: "m-2", AccountID: "", InstrumentCode: "USD", Amount: "200", BucketKey: "k2"},
		{LineNumber: 3, MeasurementID: "m-1", AccountID: "acc-3", InstrumentCode: "USD", Amount: "300", BucketKey: "k3"}, // Duplicate
	}

	for i := range rows {
		pipeline.ValidateRow(ctx, &rows[i])
	}

	summary := pipeline.Summary()
	assert.Equal(t, 3, summary.TotalRows)
	assert.Equal(t, 1, summary.ValidRows)
	assert.Equal(t, 2, summary.InvalidRows)
	assert.Equal(t, 1, summary.DuplicateCount)
}

func TestPipeline_Reset(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	pipeline, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Validate a row
	row := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "acc-1",
		InstrumentCode: "USD",
		Amount:         "100",
		BucketKey:      "k1",
	}
	pipeline.ValidateRow(ctx, row)

	summary := pipeline.Summary()
	assert.Equal(t, 1, summary.TotalRows)

	// Reset
	pipeline.Reset()

	summary = pipeline.Summary()
	assert.Equal(t, 0, summary.TotalRows)
}

func TestNewPipeline_NilDuplicateChecker(t *testing.T) {
	instChecker := &MockInstrumentChecker{}

	_, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  nil,
		InstrumentChecker: instChecker,
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrNilDuplicateChecker)
}

func TestNewPipeline_NilInstrumentChecker(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)

	_, err := NewPipeline(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: nil,
	})

	assert.Error(t, err)
}

func TestStreamingValidator(t *testing.T) {
	dupChecker := NewDuplicateChecker(DefaultBloomFilterConfig(), nil)
	instChecker := &MockInstrumentChecker{
		Instruments: map[string]*referencedatav1.InstrumentDefinition{
			"USD": {
				Code:   "USD",
				Status: referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
			},
		},
	}

	sv, err := NewStreamingValidator(PipelineConfig{
		DuplicateChecker:  dupChecker,
		InstrumentChecker: instChecker,
	})
	require.NoError(t, err)
	defer sv.Close()

	ctx := context.Background()

	// Validate rows one at a time
	row1 := &ImportRow{
		LineNumber:     1,
		MeasurementID:  "m-1",
		AccountID:      "acc-1",
		InstrumentCode: "USD",
		Amount:         "100",
		BucketKey:      "k1",
	}
	rowErr := sv.Validate(ctx, row1)
	assert.False(t, rowErr.HasErrors())

	row2 := &ImportRow{
		LineNumber:     2,
		MeasurementID:  "m-2",
		AccountID:      "",
		InstrumentCode: "USD",
		Amount:         "200",
		BucketKey:      "k2",
	}
	rowErr = sv.Validate(ctx, row2)
	assert.True(t, rowErr.HasErrors())

	summary := sv.Summary()
	assert.Equal(t, 2, summary.TotalRows)
	assert.Equal(t, 1, summary.ValidRows)
	assert.Equal(t, 1, summary.InvalidRows)
	assert.Greater(t, summary.Duration.Nanoseconds(), int64(0))
}

func TestSummary_CacheHitRate(t *testing.T) {
	summary := &Summary{
		InstrumentCacheHits:   80,
		InstrumentCacheMisses: 20,
	}

	rate := summary.CacheHitRate()
	assert.InDelta(t, 80.0, rate, 0.1)

	// Zero case
	emptySum := &Summary{}
	assert.Equal(t, 0.0, emptySum.CacheHitRate())
}

func TestSummary_BloomFilterEffectiveness(t *testing.T) {
	summary := &Summary{
		DuplicateCount:            90,
		BloomFilterFalsePositives: 10,
	}

	effectiveness := summary.BloomFilterEffectiveness()
	assert.InDelta(t, 90.0, effectiveness, 0.1)

	// Zero hits case - 100% effectiveness
	emptySum := &Summary{}
	assert.Equal(t, 100.0, emptySum.BloomFilterEffectiveness())
}
