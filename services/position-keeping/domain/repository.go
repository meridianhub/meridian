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
