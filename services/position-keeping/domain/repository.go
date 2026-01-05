package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when a financial position log is not found
	ErrNotFound = errors.New("financial position log not found")
	// ErrConflict is returned when there's a conflict (e.g., duplicate idempotency key)
	ErrConflict = errors.New("financial position log conflict")
	// ErrOptimisticLock is returned when optimistic locking fails (version mismatch)
	ErrOptimisticLock = errors.New("optimistic lock failure: resource was modified")
)

// FinancialPositionLogRepository defines the contract for persisting and retrieving
// FinancialPositionLog aggregates.
//
// All methods accept a context for cancellation and deadline control.
// The repository implementation should handle database transactions appropriately.
type FinancialPositionLogRepository interface {
	// Create persists a new FinancialPositionLog aggregate to the database.
	// Returns ErrConflict if a log with the same LogID already exists.
	Create(ctx context.Context, log *FinancialPositionLog) error

	// CreateBatch persists multiple FinancialPositionLog aggregates atomically.
	// If any log fails to persist, the entire batch is rolled back.
	// Returns ErrConflict if any log has a duplicate LogID.
	// Implementations should use efficient bulk operations (COPY or prepared statements).
	CreateBatch(ctx context.Context, logs []*FinancialPositionLog) error

	// FindByID retrieves a FinancialPositionLog by its LogID.
	// Returns ErrNotFound if the log doesn't exist.
	FindByID(ctx context.Context, logID uuid.UUID) (*FinancialPositionLog, error)

	// FindByAccountID retrieves all FinancialPositionLogs for a specific account.
	// Returns an empty slice if no logs exist for the account.
	FindByAccountID(ctx context.Context, accountID string) ([]*FinancialPositionLog, error)

	// Update updates an existing FinancialPositionLog.
	// Uses optimistic locking via the Version field.
	// Returns ErrNotFound if the log doesn't exist.
	// Returns ErrOptimisticLock if the version doesn't match (concurrent modification).
	Update(ctx context.Context, log *FinancialPositionLog) error

	// List retrieves FinancialPositionLogs matching the given filter with pagination.
	// Returns an empty slice if no logs match the filter.
	List(ctx context.Context, filter PositionLogFilter) ([]*FinancialPositionLog, error)

	// FindPendingForReconciliation retrieves logs that are pending reconciliation.
	// This is a specialized query for batch reconciliation processes.
	// The limit parameter controls the maximum number of logs returned (0 = no limit).
	FindPendingForReconciliation(ctx context.Context, limit int) ([]*FinancialPositionLog, error)
}

// PositionLogFilter defines filtering and pagination options for listing financial position logs.
type PositionLogFilter struct {
	// AccountID filters by account (optional)
	AccountID *string

	// Status filters by current status (optional)
	Status *TransactionStatus

	// ReconciliationStatus filters by reconciliation status (optional)
	ReconciliationStatus *ReconciliationStatus

	// FromDate filters logs updated after this date (optional)
	FromDate *time.Time

	// ToDate filters logs updated before this date (optional)
	ToDate *time.Time

	// Pagination options
	Limit  int // Maximum number of results (required, must be > 0)
	Offset int // Number of results to skip (default: 0)
}

// MeasurementRepository defines the contract for persisting and retrieving
// Measurement entities. All methods accept a context for cancellation and deadline control.
type MeasurementRepository interface {
	// Create persists a new Measurement to the database.
	// Returns ErrConflict if a measurement with the same ID already exists.
	Create(ctx context.Context, measurement *Measurement) error

	// FindByID retrieves a Measurement by its ID.
	// Returns ErrNotFound if the measurement doesn't exist.
	FindByID(ctx context.Context, id uuid.UUID) (*Measurement, error)

	// FindByPositionLogID retrieves all Measurements for a specific financial position log.
	// Returns an empty slice if no measurements exist for the log.
	FindByPositionLogID(ctx context.Context, positionLogID uuid.UUID) ([]*Measurement, error)
}

// PositionRepository defines the contract for persisting position records
// using an append-only write pattern.
//
// IMPORTANT: This repository enforces append-only semantics for O(1) constant-time
// inserts without locks. Position consolidation is deferred to read-time aggregation
// or background compaction (Phase 2).
//
// Design rationale:
//   - Insert() is the ONLY write method - no Update() or Upsert()
//   - Each measurement creates a new position row, never merges on write
//   - Database trigger prevents UPDATE on amount column
//   - Achieves O(1) writes without locks for high-throughput scenarios
//
// FUTURE: Background compaction (Phase 2) will be documented in ADR-00XX.
// Compaction considerations:
//   - Compacted records will be marked with a compaction_batch_id
//   - reference_id chain will be preserved for audit trail
//   - RecordCount in AggregatedPosition will reflect pre-compaction counts
type PositionRepository interface {
	// Insert persists a new Position record to the database.
	// This is the ONLY write method - append-only semantics are enforced.
	// Returns ErrConflict if a position with the same ID already exists.
	Insert(ctx context.Context, position *Position) error

	// InsertBatch persists multiple Position records atomically.
	// If any position fails to persist, the entire batch is rolled back.
	// Returns ErrConflict if any position has a duplicate ID.
	InsertBatch(ctx context.Context, positions []*Position) error

	// FindByID retrieves a Position by its ID.
	// Returns ErrNotFound if the position doesn't exist.
	FindByID(ctx context.Context, id uuid.UUID) (*Position, error)

	// GetAggregatedPosition retrieves the consolidated position for a specific
	// (AccountID, InstrumentCode, BucketKey) combination by summing all records.
	// Returns nil if no positions exist for the combination.
	GetAggregatedPosition(ctx context.Context, accountID, instrumentCode, bucketKey string) (*AggregatedPosition, error)

	// ListByAccount retrieves all position records for an account with pagination.
	// Returns an empty slice if no positions exist.
	ListByAccount(ctx context.Context, accountID string, limit, offset int) ([]*Position, error)

	// ListAggregatedByAccount retrieves all aggregated positions for an account.
	// Groups by (InstrumentCode, BucketKey) and sums amounts.
	// Returns an empty slice if no positions exist.
	ListAggregatedByAccount(ctx context.Context, accountID string) ([]*AggregatedPosition, error)

	// GetPositionCount returns the count of positions for an account.
	// This is useful for pagination and monitoring position growth.
	GetPositionCount(ctx context.Context, accountID string) (int64, error)
}
