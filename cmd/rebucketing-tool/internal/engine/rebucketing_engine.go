package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	refclient "github.com/meridianhub/meridian/services/reference-data/client"
)

// RebucketingEngine orchestrates the rebucketing process.
// It coordinates between the instrument registry, measurement streamer, and CEL evaluator
// to build a rebucketing plan.
type RebucketingEngine struct {
	refDataClient *refclient.Client
	streamer      *MeasurementStreamer
	evaluator     *CELEvaluator

	// Progress callback for reporting
	onProgress func(Progress)
}

// NewRebucketingEngine creates a new rebucketing engine with the given dependencies.
func NewRebucketingEngine(
	refDataClient *refclient.Client,
	streamer *MeasurementStreamer,
	evaluator *CELEvaluator,
) *RebucketingEngine {
	return &RebucketingEngine{
		refDataClient: refDataClient,
		streamer:      streamer,
		evaluator:     evaluator,
	}
}

// SetProgressCallback sets a callback function that will be called with progress updates.
func (e *RebucketingEngine) SetProgressCallback(callback func(Progress)) {
	e.onProgress = callback
}

// BuildRebucketingPlan analyzes measurements and builds a plan for rebucketing.
// It:
// 1. Loads the old and new instrument versions from the registry
// 2. Compiles the new CEL expression
// 3. Streams measurements and evaluates the new bucket key for each
// 4. Builds mappings from old bucket IDs to new bucket IDs
//
// The operation can be cancelled via context.
//
//nolint:gocognit,gocyclo // This is a core orchestration function that requires multiple coordinated steps.
func (e *RebucketingEngine) BuildRebucketingPlan(
	ctx context.Context,
	instrumentCode string,
	oldVersion int,
	newVersion int,
) (*RebucketingPlan, error) {
	startTime := time.Now()

	// Load both instrument versions
	oldInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, oldVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load old instrument version %d: %w",
			ErrInstrumentNotFound, oldVersion, err)
	}

	newInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, newVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load new instrument version %d: %w",
			ErrInstrumentNotFound, newVersion, err)
	}

	// Validate instruments match
	if oldInstrument.Code != newInstrument.Code {
		return nil, fmt.Errorf("%w: old=%s, new=%s",
			ErrInstrumentMismatch, oldInstrument.Code, newInstrument.Code)
	}

	// Compile the new bucket key expression
	newExpression := newInstrument.FungibilityKeyExpression
	if newExpression == "" {
		return nil, fmt.Errorf("%w: new instrument has no fungibility key expression",
			ErrInvalidCELExpression)
	}

	newProgram, err := e.evaluator.CompileBucketKeyExpression(newExpression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile new expression: %w", err)
	}

	// Initialize the plan
	plan := &RebucketingPlan{
		InstrumentCode:              instrumentCode,
		OldInstrumentVersion:        int(oldInstrument.Version),
		NewInstrumentVersion:        int(newInstrument.Version),
		OldFungibilityKeyExpression: oldInstrument.FungibilityKeyExpression,
		NewFungibilityKeyExpression: newExpression,
		BucketMappings:              make([]BucketMapping, 0),
	}

	// Use a map to collect bucket mappings
	bucketMappings := make(map[string]*bucketMappingBuilder)
	positionIDSet := make(map[uuid.UUID]struct{})
	var mu sync.Mutex

	// Stream config - filter by old bucket pattern if the old expression exists
	config := NewStreamConfig(instrumentCode)

	// Stream measurements and build the plan
	err = e.streamer.StreamMeasurements(ctx, config, func(batch []MeasurementRecord, progress Progress) (bool, error) {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		// Process batch
		for _, record := range batch {
			result := e.evaluateMeasurement(newProgram, record)

			mu.Lock()
			plan.ProcessedCount++

			if result.Error != nil {
				plan.ErrorCount++
				mu.Unlock()
				continue
			}

			// Track affected position IDs
			positionIDSet[result.FinancialPositionLogID] = struct{}{}

			// Build bucket mapping
			key := result.OldBucketID + ":" + result.NewBucketID
			builder, exists := bucketMappings[key]
			if !exists {
				builder = &bucketMappingBuilder{
					OldBucketID: result.OldBucketID,
					NewBucketID: result.NewBucketID,
					positionIDs: make(map[uuid.UUID]struct{}),
				}
				bucketMappings[key] = builder
			}

			builder.measurementIDs = append(builder.measurementIDs, result.MeasurementID)
			builder.positionIDs[result.FinancialPositionLogID] = struct{}{}
			builder.count++

			mu.Unlock()
		}

		// Update progress
		elapsed := time.Since(startTime).Seconds()
		progress.Rate = float64(plan.ProcessedCount) / elapsed
		plan.TotalCount = progress.Total

		if e.onProgress != nil {
			e.onProgress(progress)
		}

		return true, nil
	})

	if err != nil && !errors.Is(err, ErrNoMeasurementsFound) {
		return nil, err
	}

	// Convert builders to final bucket mappings
	plan.BucketMappings = make([]BucketMapping, 0, len(bucketMappings))
	for _, builder := range bucketMappings {
		mapping := BucketMapping{
			OldBucketID:      builder.OldBucketID,
			NewBucketID:      builder.NewBucketID,
			MeasurementIDs:   builder.measurementIDs,
			PositionIDs:      make([]uuid.UUID, 0, len(builder.positionIDs)),
			MeasurementCount: builder.count,
		}
		for posID := range builder.positionIDs {
			mapping.PositionIDs = append(mapping.PositionIDs, posID)
		}
		plan.BucketMappings = append(plan.BucketMappings, mapping)
	}

	// Convert position ID set to slice
	plan.AffectedPositionIDs = make([]uuid.UUID, 0, len(positionIDSet))
	for posID := range positionIDSet {
		plan.AffectedPositionIDs = append(plan.AffectedPositionIDs, posID)
	}

	return plan, nil
}

// bucketMappingBuilder accumulates measurements for a specific bucket transition.
type bucketMappingBuilder struct {
	OldBucketID    string
	NewBucketID    string
	measurementIDs []uuid.UUID
	positionIDs    map[uuid.UUID]struct{}
	count          int64
}

// evaluateMeasurement evaluates a single measurement against the new CEL expression.
func (e *RebucketingEngine) evaluateMeasurement(program cel.Program, record MeasurementRecord) RebucketingResult {
	result := RebucketingResult{
		MeasurementID:          record.ID,
		FinancialPositionLogID: record.FinancialPositionLogID,
		OldBucketID:            record.CurrentBucketID,
	}

	// Evaluate the new bucket key
	newBucketID, err := e.evaluator.EvaluateBucketKey(program, record.Metadata)
	if err != nil {
		result.Error = err
		return result
	}

	result.NewBucketID = newBucketID
	result.Changed = result.OldBucketID != result.NewBucketID

	return result
}

// PreviewRebucketing performs a dry-run analysis without streaming all measurements.
// It uses a sample of measurements to estimate the impact of the rebucketing.
// The sampleSize parameter is reserved for future sampling implementation.
func (e *RebucketingEngine) PreviewRebucketing(
	ctx context.Context,
	instrumentCode string,
	oldVersion int,
	newVersion int,
	_ int, // sampleSize - reserved for future sampling
) (*RebucketingPlan, error) {
	// Load instrument versions
	oldInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, oldVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load old instrument: %w", ErrInstrumentNotFound, err)
	}

	newInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, newVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to load new instrument: %w", ErrInstrumentNotFound, err)
	}

	// Validate new expression
	newExpression := newInstrument.FungibilityKeyExpression
	if err := e.evaluator.ValidateExpression(newExpression); err != nil {
		return nil, err
	}

	// Get bucket distribution without streaming all measurements
	bucketCounts, err := e.streamer.CountByBucketID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket distribution: %w", err)
	}

	var totalMeasurements int64
	for _, count := range bucketCounts {
		totalMeasurements += count
	}

	// Build a preview plan
	plan := &RebucketingPlan{
		InstrumentCode:              instrumentCode,
		OldInstrumentVersion:        int(oldInstrument.Version),
		NewInstrumentVersion:        int(newInstrument.Version),
		OldFungibilityKeyExpression: oldInstrument.FungibilityKeyExpression,
		NewFungibilityKeyExpression: newExpression,
		TotalCount:                  totalMeasurements,
	}

	return plan, nil
}

// ValidateInstrumentVersions validates that the instrument versions exist and are suitable
// for rebucketing.
func (e *RebucketingEngine) ValidateInstrumentVersions(
	ctx context.Context,
	instrumentCode string,
	oldVersion int,
	newVersion int,
) error {
	oldInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, oldVersion)
	if err != nil {
		return fmt.Errorf("%w: old version %d: %w", ErrInstrumentNotFound, oldVersion, err)
	}

	newInstrument, err := e.refDataClient.RetrieveInstrument(ctx, instrumentCode, newVersion)
	if err != nil {
		return fmt.Errorf("%w: new version %d: %w", ErrInstrumentNotFound, newVersion, err)
	}

	if oldInstrument.Code != newInstrument.Code {
		return fmt.Errorf("%w: codes don't match (%s vs %s)",
			ErrInstrumentMismatch, oldInstrument.Code, newInstrument.Code)
	}

	// Validate new instrument is ACTIVE
	if newInstrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
		return fmt.Errorf("%w: got status %v", ErrInstrumentNotActive, newInstrument.Status)
	}

	// Validate new expression compiles
	if newInstrument.FungibilityKeyExpression == "" {
		return fmt.Errorf("%w: new instrument has no fungibility key expression",
			ErrInvalidCELExpression)
	}

	return e.evaluator.ValidateExpression(newInstrument.FungibilityKeyExpression)
}
