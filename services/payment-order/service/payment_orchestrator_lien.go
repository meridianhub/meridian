package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	poobservability "github.com/meridianhub/meridian/services/payment-order/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ExecuteLienWithRetry executes a lien asynchronously with exponential backoff retry.
// This is called in a goroutine after a payment order is marked COMPLETED.
// The lien execution status is tracked in the payment order for reconciliation.
//
// The method:
// 1. Creates a context with timeout for the entire retry sequence
// 2. Uses exponential backoff for retries with the existing sharedclients.Retry infrastructure
// 3. Updates the payment order's lien execution status on success or final failure
// 4. Logs all attempts for monitoring and alerting
//
func (o *PaymentOrchestrator) ExecuteLienWithRetry(parentCtx context.Context, paymentOrderID uuid.UUID, lienID string) {
	// Defensive check: guard against nil currentAccountClient even though callers currently check
	if o.currentAccountClient == nil {
		o.logger.Error("ExecuteLienWithRetry called with nil currentAccountClient",
			"payment_order_id", paymentOrderID.String(),
			"lien_id", lienID)
		return
	}

	// Recover from panics to prevent silent goroutine crashes
	defer func() {
		if r := recover(); r != nil {
			o.logger.Error("panic in ExecuteLienWithRetry",
				"panic", r,
				"payment_order_id", paymentOrderID.String(),
				"lien_id", lienID)
			// Attempt to mark as FAILED to prevent stuck PENDING state
			// Use a fresh context since the original may be cancelled
			panicCtx := context.Background()
			if tenantID, hasTenant := tenant.FromContext(parentCtx); hasTenant {
				panicCtx = tenant.WithTenant(panicCtx, tenantID)
			}
			panicCtx, panicCancel := context.WithTimeout(panicCtx, 10*time.Second)
			defer panicCancel()
			po, findErr := o.repo.FindByID(panicCtx, paymentOrderID)
			if findErr != nil {
				o.logger.Error("failed to fetch payment order after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", findErr)
				return
			}
			po.SetLienExecutionFailed(fmt.Sprintf("panic: %v", r))
			if updateErr := o.repo.Update(panicCtx, po); updateErr != nil {
				o.logger.Error("failed to update payment order status after panic",
					"payment_order_id", paymentOrderID.String(),
					"error", updateErr)
			}
		}
	}()

	// Create a context with timeout for the entire retry sequence
	ctx, cancel := context.WithTimeout(parentCtx, DefaultLienExecutionRetryTimeout)
	defer cancel()

	logger := o.logger.With(
		"payment_order_id", paymentOrderID.String(),
		"lien_id", lienID,
		"operation", "execute_lien_async",
	)

	logger.Info("starting async lien execution with retry")

	// Use configured retry config or default
	retryConfig := o.lienExecutionRetryConfig
	if retryConfig == nil {
		retryConfig = &sharedclients.RetryConfig{
			MaxRetries:          DefaultLienExecutionMaxRetries,
			InitialInterval:     500 * time.Millisecond,
			MaxInterval:         defaults.DefaultRPCTimeout,
			Multiplier:          2.0,
			RandomizationFactor: 0.5,
		}
	}

	var lastErr error
	var attempts int

	// Execute with retry
	err := sharedclients.Retry(ctx, *retryConfig, func() error {
		attempts++
		logger.Info("attempting lien execution", "attempt", attempts)

		_, execErr := o.currentAccountClient.ExecuteLien(ctx, &currentaccountv1.ExecuteLienRequest{
			LienId: lienID,
		})

		if execErr != nil {
			logger.Warn("lien execution attempt failed",
				"attempt", attempts,
				"error", execErr)
			lastErr = execErr
			return execErr
		}

		logger.Info("lien execution succeeded", "attempt", attempts)
		return nil
	})

	// Update payment order with final status
	o.updateLienExecutionStatus(ctx, paymentOrderID, attempts, err, lastErr, logger)
}

// updateLienExecutionStatus updates the payment order's lien execution status after retry completion.
// This is called after all retry attempts have finished (success or failure).
// Uses distributed locking to prevent concurrent updates across service instances, combined with
// optimistic locking (version conflict retry) for additional safety.
// Note: Uses a fresh context to ensure the status update completes even if the parent context has timed out.
//
func (o *PaymentOrchestrator) updateLienExecutionStatus(
	parentCtx context.Context,
	paymentOrderID uuid.UUID,
	totalLienAttempts int,
	retryErr error,
	lastErr error,
	logger *slog.Logger,
) {
	// Use a fresh context to ensure status update isn't cancelled by parent timeout.
	// This is intentional - the parent context may have timed out during retries,
	// but we must still persist the final status for reconciliation purposes.
	updateCtx := context.Background()
	if tenantID, hasTenant := tenant.FromContext(parentCtx); hasTenant {
		updateCtx = tenant.WithTenant(updateCtx, tenantID)
	}
	updateCtx, cancel := context.WithTimeout(updateCtx, lienStatusUpdateTimeout)
	defer cancel()

	// Acquire distributed lock if lock client is configured
	// This prevents concurrent status updates across multiple service instances
	var lock Lock
	if o.lockClient != nil {
		lockKey := fmt.Sprintf("lien:execution:%s", paymentOrderID.String())
		lockStart := time.Now()

		var lockErr error
		lock, lockErr = o.lockClient.Obtain(updateCtx, lockKey, 30*time.Second)

		// Record lock wait duration
		poobservability.RecordLienExecutionLockWaitDuration(time.Since(lockStart).Seconds())

		if IsLockNotObtained(lockErr) {
			// Lock contention - another process is updating this payment order
			logger.Warn("failed to acquire distributed lock for lien execution status update",
				"payment_order_id", paymentOrderID,
				"error", "lock already held by another process")
			poobservability.RecordLienExecutionLockContention()
			return
		} else if lockErr != nil {
			// Unexpected lock error - log and continue without lock (optimistic locking will protect us)
			logger.Error("failed to obtain distributed lock for lien execution status update",
				"payment_order_id", paymentOrderID,
				"error", lockErr)
			// Continue without lock - optimistic locking still provides safety
		} else {
			// Lock acquired successfully - ensure it's released
			defer func() {
				if releaseErr := lock.Release(updateCtx); releaseErr != nil {
					logger.Error("failed to release distributed lock",
						"payment_order_id", paymentOrderID,
						"error", releaseErr)
				}
			}()
		}
	}

	for updateAttempt := 1; updateAttempt <= lienStatusUpdateMaxRetries; updateAttempt++ {
		// Apply exponential backoff for retries to reduce contention
		if updateAttempt > 1 {
			backoff := time.Duration(updateAttempt-1) * lienStatusUpdateBackoffBase
			select {
			case <-updateCtx.Done():
				logger.Error("context cancelled during update retry backoff",
					"update_attempt", updateAttempt)
				return
			case <-time.After(backoff):
			}
		}

		// Fetch the current payment order (fresh version)
		po, err := o.repo.FindByID(updateCtx, paymentOrderID)
		if err != nil {
			logger.Error("failed to fetch payment order for lien execution status update",
				"error", err,
				"update_attempt", updateAttempt)
			return
		}

		// Update lien execution tracking fields
		po.LienExecutionAttempts = totalLienAttempts

		// Determine error message if failed
		var errMsg string
		if retryErr != nil {
			// Prefer lastErr (the underlying error) over retryErr (the retry wrapper)
			if lastErr != nil {
				errMsg = lastErr.Error()
			} else {
				errMsg = retryErr.Error()
			}
		}

		// Set status on domain object
		if retryErr == nil {
			po.SetLienExecutionSucceeded()
		} else {
			po.SetLienExecutionFailed(errMsg)
		}

		// Persist the updated status
		updateErr := o.repo.Update(updateCtx, po)
		if updateErr == nil {
			// Record metrics only after successful persistence to avoid double-counting
			// on version conflict retries
			if retryErr == nil {
				logger.Info("lien execution completed successfully",
					"total_attempts", totalLienAttempts)
				poobservability.RecordLienExecution("success")
			} else {
				logger.Error("lien execution failed after all retries",
					"total_attempts", totalLienAttempts,
					"error", errMsg)
				poobservability.RecordLienExecution("failure")
				poobservability.RecordExternalServiceError("current_account", "execute_lien")
			}
			logger.Info("payment order lien execution status updated",
				"status", po.LienExecutionStatus,
				"attempts", po.LienExecutionAttempts)
			return
		}

		// Check if this is a version conflict (optimistic locking failure)
		if isVersionConflict(updateErr) {
			logger.Warn("version conflict updating lien execution status, retrying",
				"update_attempt", updateAttempt,
				"max_attempts", lienStatusUpdateMaxRetries)
			continue
		}

		// Non-recoverable error
		logger.Error("failed to update payment order lien execution status",
			"error", updateErr,
			"update_attempt", updateAttempt)
		return
	}

	// Log and record metric for exhausted retries - this will leave the payment order
	// in PENDING state which will be caught by the reconciliation query using the
	// idx_payment_orders_lien_execution partial index
	logger.Error("failed to update lien execution status after max retries due to version conflicts",
		"max_attempts", lienStatusUpdateMaxRetries,
		"payment_order_id", paymentOrderID.String())
	poobservability.RecordLienExecutionStatusUpdateExhausted()
}

// isVersionConflict checks if an error is a version conflict error
func isVersionConflict(err error) bool {
	return errors.Is(err, persistence.ErrPaymentOrderVersionConflict)
}
