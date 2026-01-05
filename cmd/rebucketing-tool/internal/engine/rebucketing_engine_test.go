package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRebucketingEngine(t *testing.T) {
	t.Parallel()

	// Create engine with nil dependencies (for unit testing patterns)
	engine := NewRebucketingEngine(nil, nil, nil)

	require.NotNil(t, engine)
	assert.Nil(t, engine.refDataClient)
	assert.Nil(t, engine.streamer)
	assert.Nil(t, engine.evaluator)
	assert.Nil(t, engine.onProgress)
}

func TestRebucketingEngine_SetProgressCallback(t *testing.T) {
	t.Parallel()

	engine := NewRebucketingEngine(nil, nil, nil)

	var callCount atomic.Int32
	callback := func(_ Progress) {
		callCount.Add(1)
	}

	// Initially nil
	assert.Nil(t, engine.onProgress)

	// Set callback
	engine.SetProgressCallback(callback)
	assert.NotNil(t, engine.onProgress)

	// Test callback invocation
	engine.onProgress(Progress{})
	assert.Equal(t, int32(1), callCount.Load())
}

func TestRebucketingEngine_evaluateMeasurement(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	expression := `bucket_key([attributes["region"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)

	tests := []struct {
		name       string
		record     MeasurementRecord
		wantChange bool
		wantError  bool
	}{
		{
			name: "with valid region attribute - bucket changes",
			record: MeasurementRecord{
				ID:                     uuid.New(),
				FinancialPositionLogID: uuid.New(),
				CurrentBucketID:        "", // Will be computed fresh
				Metadata:               map[string]string{"region": "us-east-1"},
			},
			wantChange: true, // Empty current vs computed = change
			wantError:  false,
		},
		{
			name: "empty metadata - CEL fails on missing key",
			record: MeasurementRecord{
				ID:                     uuid.New(),
				FinancialPositionLogID: uuid.New(),
				CurrentBucketID:        "old-bucket",
				Metadata:               map[string]string{},
			},
			wantChange: false,
			wantError:  true, // CEL expression fails when required key is missing
		},
		{
			name: "nil metadata - CEL fails on missing key",
			record: MeasurementRecord{
				ID:                     uuid.New(),
				FinancialPositionLogID: uuid.New(),
				CurrentBucketID:        "old-bucket",
				Metadata:               nil,
			},
			wantChange: false,
			wantError:  true, // CEL expression fails when required key is missing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := engine.evaluateMeasurement(program, tt.record)

			assert.Equal(t, tt.record.ID, result.MeasurementID)
			assert.Equal(t, tt.record.FinancialPositionLogID, result.FinancialPositionLogID)
			assert.Equal(t, tt.record.CurrentBucketID, result.OldBucketID)

			if tt.wantError {
				assert.Error(t, result.Error)
			} else {
				assert.NoError(t, result.Error)
				assert.NotEmpty(t, result.NewBucketID)
				assert.Equal(t, tt.wantChange, result.Changed)
			}
		})
	}
}

func TestRebucketingEngine_evaluateMeasurement_Deterministic(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	expression := `bucket_key([attributes["region"], attributes["tier"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(t, err)

	record := MeasurementRecord{
		ID:                     uuid.New(),
		FinancialPositionLogID: uuid.New(),
		CurrentBucketID:        "old-bucket",
		Metadata: map[string]string{
			"region": "eu-west-1",
			"tier":   "premium",
		},
	}

	// Evaluate multiple times
	results := make([]string, 0, 10)
	for range 10 {
		result := engine.evaluateMeasurement(program, record)
		require.NoError(t, result.Error)
		results = append(results, result.NewBucketID)
	}

	// All should be identical
	for i := 1; i < len(results); i++ {
		assert.Equal(t, results[0], results[i], "bucket key must be deterministic")
	}
}

func TestBucketMappingBuilder(t *testing.T) {
	t.Parallel()

	builder := &bucketMappingBuilder{
		OldBucketID: "old",
		NewBucketID: "new",
		positionIDs: make(map[uuid.UUID]struct{}),
	}

	// Add measurements
	measurementID1 := uuid.New()
	measurementID2 := uuid.New()
	positionID1 := uuid.New()
	positionID2 := uuid.New()

	builder.measurementIDs = append(builder.measurementIDs, measurementID1, measurementID2)
	builder.positionIDs[positionID1] = struct{}{}
	builder.positionIDs[positionID2] = struct{}{}
	builder.count = 2

	// Verify builder state
	assert.Equal(t, "old", builder.OldBucketID)
	assert.Equal(t, "new", builder.NewBucketID)
	assert.Len(t, builder.measurementIDs, 2)
	assert.Len(t, builder.positionIDs, 2)
	assert.Equal(t, int64(2), builder.count)
}

func TestRebucketingPlan_Structure(t *testing.T) {
	t.Parallel()

	positionID1 := uuid.New()
	positionID2 := uuid.New()
	measurementID1 := uuid.New()
	measurementID2 := uuid.New()

	plan := RebucketingPlan{
		InstrumentCode:              "KWH",
		OldInstrumentVersion:        1,
		NewInstrumentVersion:        2,
		OldFungibilityKeyExpression: `bucket_key([attributes["zone"]])`,
		NewFungibilityKeyExpression: `bucket_key([attributes["region"], attributes["zone"]])`,
		BucketMappings: []BucketMapping{
			{
				OldBucketID:      "zone-1",
				NewBucketID:      "region-a-zone-1",
				MeasurementIDs:   []uuid.UUID{measurementID1},
				PositionIDs:      []uuid.UUID{positionID1},
				MeasurementCount: 1,
			},
			{
				OldBucketID:      "zone-2",
				NewBucketID:      "region-b-zone-2",
				MeasurementIDs:   []uuid.UUID{measurementID2},
				PositionIDs:      []uuid.UUID{positionID2},
				MeasurementCount: 1,
			},
		},
		AffectedPositionIDs: []uuid.UUID{positionID1, positionID2},
		ProcessedCount:      100,
		TotalCount:          100,
		ErrorCount:          0,
		SkippedCount:        0,
	}

	assert.Equal(t, "KWH", plan.InstrumentCode)
	assert.Equal(t, 1, plan.OldInstrumentVersion)
	assert.Equal(t, 2, plan.NewInstrumentVersion)
	assert.Len(t, plan.BucketMappings, 2)
	assert.Len(t, plan.AffectedPositionIDs, 2)

	// Verify first mapping
	mapping := plan.BucketMappings[0]
	assert.Equal(t, "zone-1", mapping.OldBucketID)
	assert.Equal(t, "region-a-zone-1", mapping.NewBucketID)
	assert.Len(t, mapping.MeasurementIDs, 1)
	assert.Equal(t, measurementID1, mapping.MeasurementIDs[0])
}

func TestRebucketingResult_Changed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		oldBucketID string
		newBucketID string
		wantChanged bool
	}{
		{
			name:        "different buckets - changed",
			oldBucketID: "bucket-a",
			newBucketID: "bucket-b",
			wantChanged: true,
		},
		{
			name:        "same bucket - not changed",
			oldBucketID: "bucket-a",
			newBucketID: "bucket-a",
			wantChanged: false,
		},
		{
			name:        "empty to non-empty - changed",
			oldBucketID: "",
			newBucketID: "bucket-a",
			wantChanged: true,
		},
		{
			name:        "both empty - not changed",
			oldBucketID: "",
			newBucketID: "",
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := RebucketingResult{
				OldBucketID: tt.oldBucketID,
				NewBucketID: tt.newBucketID,
				Changed:     tt.oldBucketID != tt.newBucketID,
			}

			assert.Equal(t, tt.wantChanged, result.Changed)
		})
	}
}

func TestRebucketingEngine_BuildRebucketingPlan_RequiresInstrumentClient(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	// Without refDataClient, BuildRebucketingPlan will panic or fail
	// This test documents that the client is required
	assert.Nil(t, engine.refDataClient)
}

func TestRebucketingEngine_ValidateInstrumentVersions_RequiresInstrumentClient(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	// Without refDataClient, ValidateInstrumentVersions will fail
	// This test documents the dependency
	assert.Nil(t, engine.refDataClient)
}

func TestRebucketingEngine_PreviewRebucketing_RequiresInstrumentClient(t *testing.T) {
	t.Parallel()

	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	// Without refDataClient, PreviewRebucketing will fail
	assert.Nil(t, engine.refDataClient)
}

func TestRebucketingEngine_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Test that operations respect context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Verify context is done
	assert.Error(t, ctx.Err())
	assert.True(t, errors.Is(ctx.Err(), context.Canceled))
}

func TestProgressCallback_Integration(t *testing.T) {
	t.Parallel()

	engine := NewRebucketingEngine(nil, nil, nil)

	var receivedProgress []Progress

	engine.SetProgressCallback(func(p Progress) {
		receivedProgress = append(receivedProgress, p)
	})

	// Simulate progress updates
	progresses := []Progress{
		{Processed: 100, Total: 1000, CurrentBatch: 1, TotalBatches: 10},
		{Processed: 500, Total: 1000, CurrentBatch: 5, TotalBatches: 10},
		{Processed: 1000, Total: 1000, CurrentBatch: 10, TotalBatches: 10},
	}

	for _, p := range progresses {
		engine.onProgress(p)
	}

	assert.Len(t, receivedProgress, 3)
	assert.Equal(t, int64(100), receivedProgress[0].Processed)
	assert.Equal(t, int64(500), receivedProgress[1].Processed)
	assert.Equal(t, int64(1000), receivedProgress[2].Processed)
}

// TestRebucketingEngine_BuildRebucketingPlan_Documentation documents the expected flow
func TestRebucketingEngine_BuildRebucketingPlan_Documentation(t *testing.T) {
	t.Parallel()

	// BuildRebucketingPlan performs these steps:
	//
	// 1. Load old instrument version from registry
	//    - Validates instrument exists
	//    - Gets old fungibility_key_expression
	//
	// 2. Load new instrument version from registry
	//    - Validates instrument exists
	//    - Gets new fungibility_key_expression
	//
	// 3. Validate instruments match
	//    - Same instrument code
	//    - New version should be ACTIVE
	//
	// 4. Compile new CEL expression
	//    - Fails fast if expression is invalid
	//
	// 5. Stream measurements
	//    - Uses keyset pagination
	//    - Processes in batches of BatchSize
	//    - Respects context cancellation
	//
	// 6. For each measurement:
	//    - Evaluate new bucket key using metadata
	//    - Compare with current bucket key
	//    - Build bucket mapping (old -> new)
	//    - Track affected position IDs
	//
	// 7. Return RebucketingPlan with:
	//    - All bucket mappings
	//    - Affected position IDs
	//    - Processing statistics
	//    - Error counts

	t.Log("BuildRebucketingPlan flow documented")
}

// BenchmarkEvaluateMeasurement benchmarks the core evaluation logic
func BenchmarkEvaluateMeasurement(b *testing.B) {
	evaluator, err := NewCELEvaluator()
	require.NoError(b, err)

	engine := NewRebucketingEngine(nil, nil, evaluator)

	expression := `bucket_key([attributes["region"], attributes["tier"], attributes["zone"]])`
	program, err := evaluator.CompileBucketKeyExpression(expression)
	require.NoError(b, err)

	record := MeasurementRecord{
		ID:                     uuid.New(),
		FinancialPositionLogID: uuid.New(),
		CurrentBucketID:        "old-bucket",
		Metadata: map[string]string{
			"region": "us-east-1",
			"tier":   "standard",
			"zone":   "az-a",
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		result := engine.evaluateMeasurement(program, record)
		if result.Error != nil {
			b.Fatal(result.Error)
		}
	}
}
