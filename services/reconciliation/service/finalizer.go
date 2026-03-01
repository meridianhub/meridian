package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/messaging"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/observability"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// maxLockRetries is the maximum number of attempts to acquire a position lock.
	maxLockRetries = 3

	// initialBackoff is the initial backoff duration before retrying a lock request.
	initialBackoff = 30 * time.Second
)

// PositionLockClient defines the interface for requesting position locks
// from the Position Keeping service during settlement finalization.
type PositionLockClient interface {
	// RequestPositionLock requests a lock on positions for the given parameters.
	// Returns nil on success. Returns a gRPC FAILED_PRECONDITION error if
	// there are in-flight operations that prevent locking.
	RequestPositionLock(ctx context.Context, req PositionLockRequest) error

	// CheckPendingOperations queries PK for any pending operations in the
	// specified period that would conflict with a position lock.
	CheckPendingOperations(ctx context.Context, assetCode string, periodStart, periodEnd time.Time) (int, error)
}

// PositionLockRequest contains the parameters for a position lock request.
type PositionLockRequest struct {
	RunID       uuid.UUID
	AssetCode   string
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// PositionLockRequestedEvent is published when a position lock is requested
// during settlement finalization.
type PositionLockRequestedEvent struct {
	RunID       string `json:"run_id"`
	AccountID   string `json:"account_id"`
	Scope       string `json:"scope"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	Status      string `json:"status"`
}

// GetRunID returns the run ID for outbox event routing.
func (e PositionLockRequestedEvent) GetRunID() string { return e.RunID }

// GetAccountID returns the account ID for outbox event routing.
func (e PositionLockRequestedEvent) GetAccountID() string { return e.AccountID }

// GetScope returns the scope for outbox event routing.
func (e PositionLockRequestedEvent) GetScope() string { return e.Scope }

// GetPeriodStart returns the period start for outbox event routing.
func (e PositionLockRequestedEvent) GetPeriodStart() string { return e.PeriodStart }

// GetPeriodEnd returns the period end for outbox event routing.
func (e PositionLockRequestedEvent) GetPeriodEnd() string { return e.PeriodEnd }

// GetStatus returns the status for outbox event routing.
func (e PositionLockRequestedEvent) GetStatus() string { return e.Status }

// SettlementFinalizer handles the finalization of settlement runs by acquiring
// position locks from Position Keeping and transitioning the run to FINALIZED state.
type SettlementFinalizer struct {
	runRepo    domain.SettlementRunRepository
	snapRepo   domain.SettlementSnapshotRepository
	lockClient PositionLockClient
	publisher  EventPublisher
	logger     *slog.Logger
}

// NewSettlementFinalizer creates a new SettlementFinalizer with required dependencies.
func NewSettlementFinalizer(
	runRepo domain.SettlementRunRepository,
	snapRepo domain.SettlementSnapshotRepository,
	lockClient PositionLockClient,
	publisher EventPublisher,
	logger *slog.Logger,
) *SettlementFinalizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &SettlementFinalizer{
		runRepo:    runRepo,
		snapRepo:   snapRepo,
		lockClient: lockClient,
		publisher:  publisher,
		logger:     logger,
	}
}

// FinalizeSettlement finalizes a completed settlement run by acquiring position
// locks and transitioning the run to FINALIZED state.
//
// Authorization: Only service accounts (auth.RoleService) can finalize settlements.
// Idempotency: If the run is already FINALIZED, returns success without side effects.
//
// The process:
//  1. Validate caller has service role
//  2. Load the settlement run and validate state
//  3. If already FINALIZED, return success (idempotent)
//  4. Validate run type is FINAL
//  5. Check for pending operations in PK that would conflict
//  6. Request position lock from PK with exponential backoff
//  7. Mark run as FINALIZED and snapshots as FINAL
//  8. Publish PositionLockRequestedEvent
func (f *SettlementFinalizer) FinalizeSettlement(ctx context.Context, runID uuid.UUID) error {
	// Step 1: Authorization - only service accounts can finalize
	if err := f.requireServiceRole(ctx); err != nil {
		observability.SettlementFinalityTotal.WithLabelValues("UNAUTHORIZED").Inc()
		return err
	}

	// Step 2: Load the settlement run
	run, err := f.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("loading settlement run %s: %w", runID, err)
	}

	// Step 3: Idempotency - if already finalized, return success
	if run.Status == domain.RunStatusFinalized {
		f.logger.InfoContext(ctx, "settlement run already finalized, skipping",
			"run_id", runID)
		observability.SettlementFinalityTotal.WithLabelValues("IDEMPOTENT").Inc()
		return nil
	}

	// Step 4: Validate run is in COMPLETED state
	if run.Status != domain.RunStatusCompleted {
		return fmt.Errorf("settlement run %s is in %s state: %w",
			runID, run.Status, domain.ErrRunNotCompleted)
	}

	// Step 5: Validate run type is FINAL
	if !run.IsFinalSettlement() {
		return fmt.Errorf("settlement run %s has type %s: %w",
			runID, run.SettlementType, domain.ErrNotFinalSettlement)
	}

	f.logger.InfoContext(ctx, "starting settlement finalization",
		"run_id", runID,
		"account_id", run.AccountID,
		"period_start", run.PeriodStart,
		"period_end", run.PeriodEnd,
	)

	// Step 6: Check for pending operations that would conflict with locking
	if f.lockClient != nil {
		pendingCount, err := f.lockClient.CheckPendingOperations(
			ctx, run.AccountID, run.PeriodStart, run.PeriodEnd)
		if err != nil {
			f.logger.WarnContext(ctx, "failed to check pending operations, proceeding with lock attempt",
				"run_id", runID, "error", err)
		} else if pendingCount > 0 {
			f.logger.WarnContext(ctx, "pending operations detected before lock attempt",
				"run_id", runID, "pending_count", pendingCount)
		}
	}

	// Step 7: Request position lock with exponential backoff
	if err := f.requestLockWithRetry(ctx, run); err != nil {
		observability.SettlementFinalityTotal.WithLabelValues("LOCK_FAILED").Inc()
		f.logger.Error("settlement finalization failed: position lock not acquired",
			"run_id", runID,
			"error", err,
			"severity", "P2")
		return fmt.Errorf("position lock failed for run %s: %w", runID, err)
	}

	// Step 8: Update run status to FINALIZED
	if err := run.Finalize(); err != nil {
		return fmt.Errorf("transitioning run %s to FINALIZED: %w", runID, err)
	}
	if err := f.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("persisting FINALIZED state for run %s: %w", runID, err)
	}

	// Step 9: Mark all snapshots as FINAL settlement type
	if err := f.snapRepo.MarkRunSnapshotsFinal(ctx, runID); err != nil {
		f.logger.WarnContext(ctx, "failed to mark snapshots as FINAL",
			"run_id", runID, "error", err)
	}

	// Step 10: Publish PositionLockRequestedEvent
	if f.publisher != nil {
		event := PositionLockRequestedEvent{
			RunID:       runID.String(),
			AccountID:   run.AccountID,
			Scope:       run.Scope.String(),
			PeriodStart: run.PeriodStart.Format(time.RFC3339),
			PeriodEnd:   run.PeriodEnd.Format(time.RFC3339),
			Status:      "LOCKED",
		}
		if pubErr := f.publisher.Publish(ctx, messaging.TopicPositionLockRequested, event); pubErr != nil {
			f.logger.WarnContext(ctx, "failed to publish PositionLockRequestedEvent",
				"run_id", runID, "error", pubErr)
		}
	}

	observability.SettlementFinalityTotal.WithLabelValues("SUCCESS").Inc()

	f.logger.InfoContext(ctx, "settlement finalization completed",
		"run_id", runID,
		"account_id", run.AccountID,
	)

	return nil
}

// requestLockWithRetry attempts to acquire a position lock with exponential backoff.
// Retries on FAILED_PRECONDITION (in-flight operations) up to maxLockRetries times.
func (f *SettlementFinalizer) requestLockWithRetry(ctx context.Context, run *domain.SettlementRun) error {
	if f.lockClient == nil {
		return nil
	}

	req := PositionLockRequest{
		RunID:       run.RunID,
		AssetCode:   run.AccountID,
		PeriodStart: run.PeriodStart,
		PeriodEnd:   run.PeriodEnd,
	}

	backoff := initialBackoff

	for attempt := 1; attempt <= maxLockRetries; attempt++ {
		observability.PositionLockAttemptTotal.WithLabelValues("ATTEMPTED").Inc()

		err := f.lockClient.RequestPositionLock(ctx, req)
		if err == nil {
			observability.PositionLockAttemptTotal.WithLabelValues("SUCCESS").Inc()
			f.logger.InfoContext(ctx, "position lock acquired",
				"run_id", run.RunID,
				"attempt", attempt)
			return nil
		}

		// Check if the error is FAILED_PRECONDITION (in-flight operations)
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition {
			// Non-retryable error
			observability.PositionLockAttemptTotal.WithLabelValues("FAILED").Inc()
			return fmt.Errorf("position lock request failed (attempt %d/%d): %w",
				attempt, maxLockRetries, err)
		}

		// Retryable: in-flight operations detected
		if attempt == maxLockRetries {
			observability.PositionLockAttemptTotal.WithLabelValues("EXHAUSTED").Inc()
			return fmt.Errorf("%w: exhausted %d retries due to in-flight operations: %w",
				domain.ErrPositionLockFailed, maxLockRetries, err)
		}

		f.logger.WarnContext(ctx, "position lock failed due to in-flight operations, retrying",
			"run_id", run.RunID,
			"attempt", attempt,
			"max_retries", maxLockRetries,
			"backoff", backoff,
			"error", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting to retry position lock: %w", ctx.Err())
		case <-time.After(backoff):
		}

		// Exponential backoff: 30s -> 1min -> 2min
		backoff *= 2
	}

	return domain.ErrPositionLockFailed
}

// requireServiceRole validates that the caller has the service role required
// for settlement finalization.
func (f *SettlementFinalizer) requireServiceRole(ctx context.Context) error {
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return fmt.Errorf("%w: missing authentication context", domain.ErrUnauthorized)
	}
	if err := auth.CheckRole(claims, auth.RoleService); err != nil {
		f.logger.Warn("unauthorized settlement finalization attempt",
			"user_id", claims.UserID,
			"roles", claims.Roles)
		return fmt.Errorf("%w: settlement finalization requires service role", domain.ErrUnauthorized)
	}
	return nil
}
