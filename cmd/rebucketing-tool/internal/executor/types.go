// Package executor provides the position update executor for rebucketing operations.
// It handles batched position updates with full audit logging, maintaining the
// append-only semantics of the position table.
package executor

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// RebucketingPlan represents the plan for rebucketing positions from an old
// instrument version to a new one. This is the input from the rebucketing engine.
type RebucketingPlan struct {
	// InstrumentCode identifies the instrument being rebucketed
	InstrumentCode string

	// OldInstrumentVersion is the version hash before rebucketing
	OldInstrumentVersion string

	// NewInstrumentVersion is the version hash after rebucketing
	NewInstrumentVersion string

	// BucketMappings maps old bucket keys to new bucket keys
	// Key: old bucket_key, Value: new bucket_key
	BucketMappings map[string]string

	// AffectedPositions lists all positions that need rebucketing
	AffectedPositions []AffectedPosition
}

// AffectedPosition represents a position that will be affected by rebucketing.
type AffectedPosition struct {
	// PositionID is the database ID of the existing position
	PositionID uuid.UUID

	// AccountID identifies the account
	AccountID string

	// InstrumentCode identifies the instrument
	InstrumentCode string

	// OldBucketKey is the current bucket_key
	OldBucketKey string

	// NewBucketKey is the target bucket_key after rebucketing
	NewBucketKey string

	// Amount is the position amount
	Amount decimal.Decimal

	// Dimension classifies the asset type
	Dimension string

	// Attributes stores flexible metadata
	Attributes map[string]string

	// ReferenceID links to the source event
	ReferenceID uuid.UUID

	// CreatedAt is when the original position was created
	CreatedAt time.Time

	// CreatedBy is who created the original position
	CreatedBy string
}

// ExecutionResult contains the results of a rebucketing execution.
type ExecutionResult struct {
	// Success indicates whether the execution completed successfully
	Success bool

	// PositionsUpdated is the count of positions that were rebucketed
	PositionsUpdated int64

	// BucketsAffected is the count of unique bucket mappings applied
	BucketsAffected int

	// AuditLogEntries is the total number of audit log entries created
	// (2 per position: SOFT_DELETE + INSERT_NEW)
	AuditLogEntries int64

	// Duration is the total execution time
	Duration time.Duration

	// DryRun indicates if this was a dry-run execution (no actual changes)
	DryRun bool

	// Error contains any error that occurred during execution
	Error error

	// PartialProgress tracks progress if execution was interrupted
	PartialProgress *PartialProgress
}

// PartialProgress tracks progress when execution is interrupted.
type PartialProgress struct {
	// PositionsProcessed is the count processed before interruption
	PositionsProcessed int64

	// LastProcessedPositionID is the ID of the last successfully processed position
	LastProcessedPositionID uuid.UUID

	// BatchesCompleted is the number of batches fully committed
	BatchesCompleted int
}

// DryRunPlan represents the output of a dry-run execution.
type DryRunPlan struct {
	// InstrumentCode identifies the instrument being rebucketed
	InstrumentCode string

	// OldInstrumentVersion is the version hash before rebucketing
	OldInstrumentVersion string

	// NewInstrumentVersion is the version hash after rebucketing
	NewInstrumentVersion string

	// AffectedPositionCount is the total number of positions that would be updated
	AffectedPositionCount int64

	// BucketMappings shows the old->new bucket key mappings
	BucketMappings map[string]string

	// BucketSummary summarizes positions per bucket mapping
	BucketSummary []BucketMappingSummary

	// EstimatedAuditEntries is the expected audit log entries (2x positions)
	EstimatedAuditEntries int64

	// EstimatedBatches is the number of batches that would be processed
	EstimatedBatches int
}

// BucketMappingSummary summarizes positions for a specific bucket mapping.
type BucketMappingSummary struct {
	// OldBucketKey is the source bucket key
	OldBucketKey string

	// NewBucketKey is the target bucket key
	NewBucketKey string

	// PositionCount is the number of positions with this mapping
	PositionCount int64

	// TotalAmount is the sum of amounts for positions with this mapping
	TotalAmount decimal.Decimal
}

// AuditLogEntry represents a single entry in the rebucketing audit log.
type AuditLogEntry struct {
	// ID is the unique identifier for this audit entry
	ID uuid.UUID

	// Timestamp is when the audit entry was created
	Timestamp time.Time

	// AdminUserID is the admin who authorized the rebucketing
	AdminUserID string

	// OldInstrumentVersion is the version hash before rebucketing
	OldInstrumentVersion string

	// NewInstrumentVersion is the version hash after rebucketing
	NewInstrumentVersion string

	// PositionID is the ID of the affected position
	PositionID uuid.UUID

	// OldBucketID is the bucket_key before rebucketing
	OldBucketID string

	// NewBucketID is the bucket_key after rebucketing
	NewBucketID string

	// Operation is the type of operation (SOFT_DELETE or INSERT_NEW)
	Operation AuditOperation
}

// AuditOperation represents the type of audit log operation.
type AuditOperation string

const (
	// AuditOperationSoftDelete marks the old position as deleted
	AuditOperationSoftDelete AuditOperation = "SOFT_DELETE"

	// AuditOperationInsertNew creates a new position with corrected bucket_key
	AuditOperationInsertNew AuditOperation = "INSERT_NEW"
)

// String returns the string representation of an AuditOperation.
func (op AuditOperation) String() string {
	return string(op)
}

// IsValid checks if the operation is a valid audit operation.
func (op AuditOperation) IsValid() bool {
	switch op {
	case AuditOperationSoftDelete, AuditOperationInsertNew:
		return true
	default:
		return false
	}
}

// Config holds configuration for the position update executor.
type Config struct {
	// BatchSize is the number of positions to process per batch
	// Default: 500
	BatchSize int

	// DryRun mode shows plan without executing
	DryRun bool
}

// DefaultConfig returns the default executor configuration.
func DefaultConfig() *Config {
	return &Config{
		BatchSize: 500,
		DryRun:    false,
	}
}

// Validate validates the executor configuration.
func (c *Config) Validate() error {
	if c.BatchSize <= 0 {
		return ErrInvalidBatchSize
	}
	if c.BatchSize > 10000 {
		return ErrBatchSizeTooLarge
	}
	return nil
}
