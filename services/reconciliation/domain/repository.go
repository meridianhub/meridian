package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SettlementRunRepository defines the contract for persisting and retrieving settlement runs.
type SettlementRunRepository interface {
	// Create persists a new SettlementRun.
	// Returns ErrConflict if a run with the same RunID already exists.
	Create(ctx context.Context, run *SettlementRun) error

	// FindByID retrieves a SettlementRun by its RunID.
	// Returns ErrNotFound if the run doesn't exist.
	FindByID(ctx context.Context, runID uuid.UUID) (*SettlementRun, error)

	// Update updates an existing SettlementRun using optimistic locking.
	// Returns ErrNotFound if the run doesn't exist.
	// Returns ErrOptimisticLock if the version doesn't match.
	Update(ctx context.Context, run *SettlementRun) error

	// List retrieves settlement runs matching the given filter with pagination.
	List(ctx context.Context, filter RunFilter) ([]*SettlementRun, error)
}

// RunFilter defines filtering and pagination for settlement runs.
type RunFilter struct {
	AccountID *string
	Status    *RunStatus
	Scope     *ReconciliationScope
	FromDate  *time.Time
	ToDate    *time.Time
	Limit     int
	Offset    int
}

// SettlementSnapshotRepository defines the contract for persisting settlement snapshots.
type SettlementSnapshotRepository interface {
	// Create persists a new SettlementSnapshot.
	Create(ctx context.Context, snapshot *SettlementSnapshot) error

	// CreateBatch persists multiple snapshots atomically.
	CreateBatch(ctx context.Context, snapshots []*SettlementSnapshot) error

	// FindByID retrieves a SettlementSnapshot by its SnapshotID.
	// Returns ErrNotFound if the snapshot doesn't exist.
	FindByID(ctx context.Context, snapshotID uuid.UUID) (*SettlementSnapshot, error)

	// FindByRunID retrieves all snapshots for a settlement run.
	FindByRunID(ctx context.Context, runID uuid.UUID) ([]*SettlementSnapshot, error)

	// DeleteByRunID removes all snapshots for a given settlement run.
	// Used for idempotent cleanup before retrying a failed capture.
	DeleteByRunID(ctx context.Context, runID uuid.UUID) error
}

// VarianceRepository defines the contract for persisting and retrieving variances.
type VarianceRepository interface {
	// Create persists a new Variance.
	Create(ctx context.Context, variance *Variance) error

	// CreateBatch persists multiple variances atomically.
	CreateBatch(ctx context.Context, variances []*Variance) error

	// FindByID retrieves a Variance by its VarianceID.
	// Returns ErrNotFound if the variance doesn't exist.
	FindByID(ctx context.Context, varianceID uuid.UUID) (*Variance, error)

	// FindByRunID retrieves all variances for a settlement run.
	FindByRunID(ctx context.Context, runID uuid.UUID) ([]*Variance, error)

	// Update updates an existing Variance.
	// Returns ErrNotFound if the variance doesn't exist.
	Update(ctx context.Context, variance *Variance) error

	// List retrieves variances matching the given filter.
	List(ctx context.Context, filter VarianceFilter) ([]*Variance, error)
}

// VarianceFilter defines filtering and pagination for variances.
type VarianceFilter struct {
	RunID     *uuid.UUID
	AccountID *string
	Status    *VarianceStatus
	Reason    *VarianceReason
	Limit     int
	Offset    int
}

// DisputeRepository defines the contract for persisting and retrieving disputes.
type DisputeRepository interface {
	// Create persists a new Dispute.
	Create(ctx context.Context, dispute *Dispute) error

	// FindByID retrieves a Dispute by its DisputeID.
	// Returns ErrNotFound if the dispute doesn't exist.
	FindByID(ctx context.Context, disputeID uuid.UUID) (*Dispute, error)

	// FindByVarianceID retrieves all disputes for a variance.
	FindByVarianceID(ctx context.Context, varianceID uuid.UUID) ([]*Dispute, error)

	// Update updates an existing Dispute.
	// Returns ErrNotFound if the dispute doesn't exist.
	Update(ctx context.Context, dispute *Dispute) error

	// List retrieves disputes matching the given filter.
	List(ctx context.Context, filter DisputeFilter) ([]*Dispute, error)
}

// DisputeFilter defines filtering and pagination for disputes.
type DisputeFilter struct {
	RunID     *uuid.UUID
	AccountID *string
	Status    *DisputeStatus
	Limit     int
	Offset    int
}

// BalanceAssertionRepository defines the contract for persisting balance assertions.
type BalanceAssertionRepository interface {
	// Create persists a new BalanceAssertion.
	Create(ctx context.Context, assertion *BalanceAssertion) error

	// FindByID retrieves a BalanceAssertion by its AssertionID.
	// Returns ErrNotFound if the assertion doesn't exist.
	FindByID(ctx context.Context, assertionID uuid.UUID) (*BalanceAssertion, error)

	// FindByRunID retrieves all assertions for a settlement run.
	FindByRunID(ctx context.Context, runID uuid.UUID) ([]*BalanceAssertion, error)

	// Update updates an existing BalanceAssertion.
	// Returns ErrNotFound if the assertion doesn't exist.
	Update(ctx context.Context, assertion *BalanceAssertion) error

	// List retrieves assertions matching the given filter.
	List(ctx context.Context, filter AssertionFilter) ([]*BalanceAssertion, error)
}

// AssertionFilter defines filtering and pagination for balance assertions.
type AssertionFilter struct {
	RunID     *uuid.UUID
	AccountID *string
	Status    *AssertionStatus
	Limit     int
	Offset    int
}
