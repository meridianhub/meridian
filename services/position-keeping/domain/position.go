package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Position domain errors
var (
	// ErrEmptyInstrumentCode is returned when instrument code is empty
	ErrEmptyInstrumentCode = errors.New("instrument code cannot be empty")
	// ErrEmptyBucketKey is returned when bucket key is empty
	ErrEmptyBucketKey = errors.New("bucket key cannot be empty")
	// ErrPositionUpdateForbidden is returned when attempting to update a position (append-only)
	ErrPositionUpdateForbidden = errors.New("position updates are forbidden: append-only mode enforced")
)

// Position represents a single position record in the append-only positions table.
// Each position record is immutable once written - new measurements create new rows.
// Position consolidation is deferred to read-time or background compaction.
//
// This implements the append-only write pattern for O(1) constant-time inserts
// without locks, enabling high-throughput position tracking for multi-asset systems.
type Position struct {
	// ID is the unique database identifier for this position record
	ID uuid.UUID

	// AccountID identifies the account this position belongs to
	AccountID string

	// InstrumentCode identifies the asset type (e.g., "GBP", "KWH", "GPU_HOUR")
	InstrumentCode string

	// BucketKey is the fungibility key for position aggregation
	// Positions with the same (AccountID, InstrumentCode, BucketKey) are fungible
	BucketKey string

	// Amount is the quantity for this position record
	// Positive for credits/additions, negative for debits/reductions
	Amount decimal.Decimal

	// Dimension classifies the asset type (Monetary, Energy, Compute, etc.)
	Dimension string

	// Attributes stores flexible metadata for CEL-based fungibility key generation
	Attributes map[string]string

	// ReferenceID links this position to the source event (measurement ID, transaction ID, etc.)
	ReferenceID uuid.UUID

	// CreatedAt is when this position record was created
	CreatedAt time.Time

	// CreatedBy is the user/system that created this record
	CreatedBy string
}

// NewPosition creates a new Position record with validation.
// Returns an error if:
//   - accountID is empty
//   - instrumentCode is empty
//   - bucketKey is empty
func NewPosition(
	accountID string,
	instrumentCode string,
	bucketKey string,
	amount decimal.Decimal,
	dimension string,
	attributes map[string]string,
	referenceID uuid.UUID,
	createdBy string,
) (*Position, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if instrumentCode == "" {
		return nil, ErrEmptyInstrumentCode
	}
	if bucketKey == "" {
		return nil, ErrEmptyBucketKey
	}

	now := time.Now().UTC()
	return &Position{
		ID:             uuid.New(),
		AccountID:      accountID,
		InstrumentCode: instrumentCode,
		BucketKey:      bucketKey,
		Amount:         amount,
		Dimension:      dimension,
		Attributes:     attributes,
		ReferenceID:    referenceID,
		CreatedAt:      now,
		CreatedBy:      createdBy,
	}, nil
}

// AggregatedPosition represents the consolidated view of positions
// for a specific (AccountID, InstrumentCode, BucketKey) combination.
// This is computed at read-time by summing all Position records.
type AggregatedPosition struct {
	// AccountID identifies the account
	AccountID string

	// InstrumentCode identifies the asset type
	InstrumentCode string

	// BucketKey is the fungibility key
	BucketKey string

	// TotalAmount is the sum of all position amounts
	TotalAmount decimal.Decimal

	// Dimension classifies the asset type
	Dimension string

	// RecordCount is the number of position records contributing to this aggregate
	RecordCount int64

	// LastUpdated is the timestamp of the most recent position record
	LastUpdated time.Time
}
