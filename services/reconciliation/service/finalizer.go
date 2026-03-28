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
	if err := f.requireServiceRole(ctx); err != nil {
		observability.SettlementFinalityTotal.WithLabelValues("UNAUTHORIZED").Inc()
		return err
	}

	run, err := f.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("loading settlement run %s: %w", runID, err)
	}

	skip, err := f.validateFinalizable(ctx, run)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	f.logger.InfoContext(ctx, "starting settlement finalization",
		"run_id", runID,
		"account_id", run.AccountID,
		"period_start", run.PeriodStart,
		"period_end", run.PeriodEnd,
	)

	f.checkPendingOperations(ctx, run)

	if err := f.requestLockWithRetry(ctx, run); err != nil {
		observability.SettlementFinalityTotal.WithLabelValues("LOCK_FAILED").Inc()
		f.logger.Error("settlement finalization failed: position lock not acquired",
			"run_id", runID,
			"error", err,
			"severity", "P2")
		return fmt.Errorf("position lock failed for run %s: %w", runID, err)
	}

	if err := f.markFinalized(ctx, run); err != nil {
		return err
	}

	f.publishLockEvent(ctx, run)

	observability.SettlementFinalityTotal.WithLabelValues("SUCCESS").Inc()

	f.logger.InfoContext(ctx, "settlement finalization completed",
		"run_id", runID,
		"account_id", run.AccountID,
	)

	return nil
}

// validateFinalizable checks that a run is in a state that allows finalization.
// Returns (true, nil) if the run is already finalized (caller should skip).
// Returns (false, nil) if the run is ready to finalize.
// Returns (false, error) if the run cannot be finalized.
func (f *SettlementFinalizer) validateFinalizable(ctx context.Context, run *domain.SettlementRun) (bool, error) {
	if run.Status == domain.RunStatusFinalized {
		f.logger.InfoContext(ctx, "settlement run already finalized, skipping",
			"run_id", run.RunID)
		observability.SettlementFinalityTotal.WithLabelValues("IDEMPOTENT").Inc()
		return true, nil
	}

	if run.Status != domain.RunStatusCompleted {
		return false, fmt.Errorf("settlement run %s is in %s state: %w",
			run.RunID, run.Status, domain.ErrRunNotCompleted)
	}

	if !run.IsFinalSettlement() {
		return false, fmt.Errorf("settlement run %s has type %s: %w",
			run.RunID, run.SettlementType, domain.ErrNotFinalSettlement)
	}

	return false, nil
}

// checkPendingOperations logs a warning if PK has in-flight operations that
// may conflict with position locking.
func (f *SettlementFinalizer) checkPendingOperations(ctx context.Context, run *domain.SettlementRun) {
	if f.lockClient == nil {
		return
	}
	pendingCount, err := f.lockClient.CheckPendingOperations(
		ctx, run.AccountID, run.PeriodStart, run.PeriodEnd)
	if err != nil {
		f.logger.WarnContext(ctx, "failed to check pending operations, proceeding with lock attempt",
			"run_id", run.RunID, "error", err)
	} else if pendingCount > 0 {
		f.logger.WarnContext(ctx, "pending operations detected before lock attempt",
			"run_id", run.RunID, "pending_count", pendingCount)
	}
}

// markFinalized transitions the run to FINALIZED and marks snapshots as FINAL.
func (f *SettlementFinalizer) markFinalized(ctx context.Context, run *domain.SettlementRun) error {
	if err := run.Finalize(); err != nil {
		return fmt.Errorf("transitioning run %s to FINALIZED: %w", run.RunID, err)
	}
	if err := f.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("persisting FINALIZED state for run %s: %w", run.RunID, err)
	}

	if err := f.snapRepo.MarkRunSnapshotsFinal(ctx, run.RunID); err != nil {
		f.logger.WarnContext(ctx, "failed to mark snapshots as FINAL",
			"run_id", run.RunID, "error", err)
	}

	return nil
}

// publishLockEvent publishes a PositionLockRequestedEvent after successful finalization.
func (f *SettlementFinalizer) publishLockEvent(ctx context.Context, run *domain.SettlementRun) {
	if f.publisher == nil {
		return
	}
	event := PositionLockRequestedEvent{
		RunID:       run.RunID.String(),
		AccountID:   run.AccountID,
		Scope:       run.Scope.String(),
		PeriodStart: run.PeriodStart.Format(time.RFC3339),
		PeriodEnd:   run.PeriodEnd.Format(time.RFC3339),
		Status:      "LOCKED",
	}
	if pubErr := f.publisher.Publish(ctx, messaging.TopicPositionLockRequested, event); pubErr != nil {
		f.logger.WarnContext(ctx, "failed to publish PositionLockRequestedEvent",
			"run_id", run.RunID, "error", pubErr)
	}
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
