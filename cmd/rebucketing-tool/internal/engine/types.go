// Package engine provides the core rebucketing logic for migrating measurements
// from one instrument version's bucket key expression to another.
package engine

import (
	"errors"

	"github.com/google/uuid"
)

// Error types for the rebucketing engine.
var (
	// ErrInvalidCELExpression is returned when the new CEL expression fails to compile.
	ErrInvalidCELExpression = errors.New("invalid CEL expression")

	// ErrCELEvaluation is returned when CEL expression evaluation fails on specific attributes.
	ErrCELEvaluation = errors.New("CEL expression evaluation failed")

	// ErrMeasurementStream is returned when measurement streaming encounters pagination or query issues.
	ErrMeasurementStream = errors.New("measurement stream error")

	// ErrInstrumentNotFound is returned when the requested instrument version cannot be found.
	ErrInstrumentNotFound = errors.New("instrument not found")

	// ErrInstrumentMismatch is returned when old and new instruments have different codes.
	ErrInstrumentMismatch = errors.New("instrument code mismatch between versions")

	// ErrNoMeasurementsFound is returned when no measurements exist for the given criteria.
	ErrNoMeasurementsFound = errors.New("no measurements found")

	// ErrInstrumentNotActive is returned when the new instrument version is not in ACTIVE status.
	ErrInstrumentNotActive = errors.New("instrument must be in ACTIVE status")
)

// RebucketingPlan contains the analysis of how measurements will be rebucketed
// from the old instrument version to the new version.
type RebucketingPlan struct {
	// InstrumentCode is the instrument being rebucketed (e.g., "KWH", "GPU_HOURS").
	InstrumentCode string

	// OldInstrumentVersion is the version of the instrument with the old bucket key expression.
	OldInstrumentVersion int

	// NewInstrumentVersion is the version of the instrument with the corrected bucket key expression.
	NewInstrumentVersion int

	// OldFungibilityKeyExpression is the CEL expression from the old instrument version.
	OldFungibilityKeyExpression string

	// NewFungibilityKeyExpression is the CEL expression from the new instrument version.
	NewFungibilityKeyExpression string

	// BucketMappings contains the mapping from old bucket IDs to new bucket IDs.
	BucketMappings []BucketMapping

	// AffectedPositionIDs contains all unique financial position log IDs with affected measurements.
	AffectedPositionIDs []uuid.UUID

	// ProcessedCount is the number of measurements processed so far.
	ProcessedCount int64

	// TotalCount is the total number of measurements to process.
	TotalCount int64

	// ErrorCount is the number of measurements that failed CEL evaluation.
	ErrorCount int64

	// SkippedCount is the number of measurements skipped (e.g., missing metadata).
	SkippedCount int64
}

// BucketMapping represents the migration path for measurements from one bucket to another.
type BucketMapping struct {
	// OldBucketID is the bucket key computed using the old CEL expression.
	OldBucketID string

	// NewBucketID is the bucket key computed using the new CEL expression.
	NewBucketID string

	// MeasurementIDs contains all measurement IDs that will be migrated.
	MeasurementIDs []uuid.UUID

	// PositionIDs contains all unique financial position log IDs for these measurements.
	PositionIDs []uuid.UUID

	// MeasurementCount is the number of measurements in this bucket mapping.
	MeasurementCount int64
}

// Progress represents the current progress of the rebucketing operation.
type Progress struct {
	// Processed is the number of measurements processed so far.
	Processed int64

	// Total is the total number of measurements to process.
	Total int64

	// CurrentBatch is the current batch number being processed.
	CurrentBatch int

	// TotalBatches is the estimated total number of batches.
	TotalBatches int

	// Rate is the processing rate in measurements per second.
	Rate float64
}

// StreamConfig configures the measurement streaming behavior.
type StreamConfig struct {
	// BatchSize is the number of measurements to fetch per query (default: 1000).
	BatchSize int

	// InstrumentCode filters measurements by instrument code.
	InstrumentCode string

	// OldBucketIDFilter optionally filters measurements by their current bucket ID.
	// If empty, all measurements for the instrument are streamed.
	OldBucketIDFilter string
}

// DefaultBatchSize is the default number of measurements to fetch per paginated query.
const DefaultBatchSize = 1000

// NewStreamConfig creates a StreamConfig with default values.
func NewStreamConfig(instrumentCode string) StreamConfig {
	return StreamConfig{
		BatchSize:      DefaultBatchSize,
		InstrumentCode: instrumentCode,
	}
}

// MeasurementRecord represents a measurement with its original attributes for rebucketing.
// This is a lightweight structure for streaming without full domain validation.
type MeasurementRecord struct {
	// ID is the measurement's unique identifier.
	ID uuid.UUID

	// FinancialPositionLogID is the position log this measurement belongs to.
	FinancialPositionLogID uuid.UUID

	// CurrentBucketID is the current bucket key for this measurement.
	CurrentBucketID string

	// Metadata contains the original attributes used for bucket key computation.
	Metadata map[string]string
}

// RebucketingResult represents the outcome of evaluating a single measurement.
type RebucketingResult struct {
	// MeasurementID is the ID of the measurement that was evaluated.
	MeasurementID uuid.UUID

	// FinancialPositionLogID is the position log this measurement belongs to.
	FinancialPositionLogID uuid.UUID

	// OldBucketID is the current bucket key.
	OldBucketID string

	// NewBucketID is the newly computed bucket key.
	NewBucketID string

	// Changed indicates whether the bucket key changed.
	Changed bool

	// Error is non-nil if CEL evaluation failed for this measurement.
	Error error
}
