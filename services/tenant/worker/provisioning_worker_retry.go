// Package worker - retry and backoff machinery for tenant schema provisioning.
package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/observability"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Retry configuration constants for provisioning with exponential backoff.
// These constants are deprecated in favor of WorkerConfig fields.
// They remain for backwards compatibility with existing tests.
const (
	maxRetries = 5
	baseDelay  = 2 * time.Second
	maxDelay   = defaults.DefaultRPCTimeout
)

// provisionTenantWithRetry provisions a tenant's schema with exponential backoff retry logic.
// It handles transient failures (like Atlas lock timeouts or DB connection issues) by retrying
// with increasing delays. On success, marks the tenant as active. On permanent failure or
// exhausted retries, marks the tenant as provisioning_failed with error details.
//
// The function includes panic recovery to prevent crashes and proper goroutine lifecycle management.
func (w *ProvisioningWorker) provisionTenantWithRetry(ctx context.Context, tenantID tenant.TenantID) {
	// Ensure we decrement the WaitGroup when this goroutine completes
	defer w.wg.Done()

	// Start timing the provisioning operation
	start := time.Now()
	var status string

	// Defer metric recording to ensure it happens even on panic
	defer func() {
		if status == "" {
			status = observability.StatusError // Default to error if status not set
		}
		observability.RecordProvisioningDuration(status, time.Since(start))
	}()

	// Panic recovery to prevent a single tenant provisioning failure from crashing the worker
	defer func() {
		if r := recover(); r != nil {
			status = observability.StatusError
			w.logger.Error("panic during tenant provisioning",
				"tenant_id", tenantID,
				"panic", r)
			// Mark tenant as failed to prevent it from being stuck in PROVISIONING status
			panicErr := fmt.Errorf("%w: %v", ErrPanicDuringProvision, r)
			w.markTenantAsFailed(ctx, tenantID, panicErr, 1)
		}
	}()

	attempts, lastErr := w.executeProvisioningWithRetry(ctx, tenantID)
	if lastErr == nil {
		status = observability.StatusSuccess
		return // Success
	}

	// Mark as failed
	status = observability.StatusError
	w.markTenantAsFailed(ctx, tenantID, lastErr, attempts)
}

// executeProvisioningWithRetry performs the provisioning with retry loop.
// Returns attempt count and nil on success, or attempt count and error on failure.
func (w *ProvisioningWorker) executeProvisioningWithRetry(ctx context.Context, tenantID tenant.TenantID) (int, error) {
	var lastErr error
	var attempts int

	for attempt := 0; attempt < w.maxRetries; attempt++ {
		attempts = attempt + 1
		if cancelled := w.checkContextCancellation(ctx, tenantID, attempts); cancelled {
			return 0, nil // Context cancelled, don't mark as failed
		}

		err := w.provisioner.ProvisionSchemas(ctx, tenantID)
		if err == nil {
			if hookErr := w.markTenantAsActive(ctx, tenantID, attempts); hookErr != nil {
				return attempts, hookErr
			}
			return 0, nil
		}

		lastErr = err
		if !isRetryableError(err) {
			w.logger.Error("provisioning failed with non-retryable error",
				"tenant_id", tenantID,
				"attempt", attempts,
				"error", err)
			break // Permanent error, don't retry
		}

		// Record retry attempt for observability (starting from second attempt)
		if attempt > 0 {
			observability.IncrementRetryAttempt()
		}

		if cancelled := w.waitWithBackoff(ctx, tenantID, attempt, lastErr); cancelled {
			return 0, nil // Context cancelled, don't mark as failed
		}
	}

	return attempts, lastErr
}

// checkContextCancellation checks if context is cancelled and logs appropriately.
// Returns true if cancelled, false otherwise.
func (w *ProvisioningWorker) checkContextCancellation(ctx context.Context, tenantID tenant.TenantID, attempt int) bool {
	select {
	case <-ctx.Done():
		w.logger.Warn("provisioning cancelled",
			"tenant_id", tenantID,
			"attempt", attempt,
			"error", ctx.Err())
		return true
	default:
		return false
	}
}

// markTenantAsActive updates tenant status to active after successful provisioning.
// Before marking as active, it executes any registered post-provisioning hooks.
// Hook failures are fatal - they prevent tenant activation.
// Returns nil on success, or an error if hooks or status update failed.
func (w *ProvisioningWorker) markTenantAsActive(ctx context.Context, tenantID tenant.TenantID, attempt int) error {
	w.logger.Info("provisioning succeeded",
		"tenant_id", tenantID,
		"attempt", attempt)

	// Execute post-provisioning hooks (fail-hard)
	// Hook failures prevent tenant activation to ensure complete reference data.
	if err := w.executePostProvisioningHooks(ctx, tenantID); err != nil {
		return fmt.Errorf("post-provisioning hooks: %w", err)
	}

	tenant, getErr := w.repo.GetByID(ctx, tenantID)
	if getErr != nil {
		w.logger.Error("failed to get tenant after successful provisioning",
			"tenant_id", tenantID,
			"error", getErr)
		return nil // Schema is provisioned, status update failure is non-fatal
	}

	_, updateErr := w.repo.UpdateStatus(ctx, tenantID, domain.StatusActive, tenant.Version)
	if updateErr != nil {
		w.logger.Error("failed to mark tenant active",
			"tenant_id", tenantID,
			"version", tenant.Version,
			"error", updateErr)
	}
	return nil
}

// markTenantAsFailed updates tenant status to provisioning_failed with error details.
// Service-specific failure tracking is implemented in postgres_provisioner.go:provisionAllServices()
// which calls observability.IncrementServiceFailure(serviceName) for each service failure type:
// database connection errors, circuit breaker open/half-open states, and migration failures.
func (w *ProvisioningWorker) markTenantAsFailed(ctx context.Context, tenantID tenant.TenantID, lastErr error, attempts int) {
	tenant, getErr := w.repo.GetByID(ctx, tenantID)
	if getErr != nil {
		w.logger.Error("failed to get tenant for failure update",
			"tenant_id", tenantID,
			"error", getErr)
		return
	}

	_, updateErr := w.repo.UpdateStatusWithError(ctx, tenantID, domain.StatusProvisioningFailed, lastErr.Error(), tenant.Version)
	if updateErr != nil {
		w.logger.Error("failed to mark tenant as provisioning_failed",
			"tenant_id", tenantID,
			"version", tenant.Version,
			"error", updateErr)
	}

	w.logger.Error("provisioning failed after retries",
		"tenant_id", tenantID,
		"attempts", attempts,
		"error", lastErr)
}

// waitWithBackoff waits for the calculated backoff duration with context cancellation support.
// Returns true if cancelled, false otherwise.
func (w *ProvisioningWorker) waitWithBackoff(ctx context.Context, tenantID tenant.TenantID, attempt int, err error) bool {
	delay := w.calculateBackoffDelay(attempt)

	w.logger.Warn("provisioning failed, retrying",
		"tenant_id", tenantID,
		"attempt", attempt+1,
		"delay", delay,
		"error", err)

	select {
	case <-ctx.Done():
		w.logger.Warn("provisioning cancelled during backoff",
			"tenant_id", tenantID,
			"attempt", attempt+1,
			"error", ctx.Err())
		return true
	case <-time.After(delay):
		return false
	}
}

// calculateBackoffDelay calculates exponential backoff delay with jitter.
// The delay is capped at w.retryMaxDelay (including jitter) to ensure predictable maximum wait times.
func (w *ProvisioningWorker) calculateBackoffDelay(attempt int) time.Duration {
	delay := time.Duration(float64(w.retryBaseDelay) * math.Pow(2, float64(attempt)))
	jitter := time.Duration(rand.Int63n(int64(delay / 4))) // Add jitter (up to 25% of delay)
	delay = delay + jitter
	if delay > w.retryMaxDelay {
		delay = w.retryMaxDelay
	}
	return delay
}

// retryablePatterns are substrings in error messages that indicate transient errors
// that are worth retrying. These typically include:
//   - Lock contention: Atlas migration locks, database row locks, etc.
//   - Connection issues: Pool exhaustion, network timeouts, etc.
//   - Temporary failures: Service temporarily unavailable, resource exhausted, etc.
var retryablePatterns = []string{
	"timeout",     // Connection timeout, lock timeout, query timeout
	"connection",  // Connection refused, connection reset, connection pool exhausted
	"lock",        // Atlas lock timeout, database row lock, advisory lock
	"temporary",   // Temporary failure, temporary unavailable
	"unavailable", // Service unavailable, resource unavailable
	"reset",       // Connection reset by peer
	"refused",     // Connection refused
	"pool",        // Connection pool exhausted
	"exhausted",   // Resource exhausted
	"retry",       // Explicit retry suggestion in error
}

// permanentPatterns are substrings in error messages that indicate permanent errors
// that should NOT be retried. These typically include:
//   - Validation errors: Invalid input, constraint violations
//   - Authorization errors: Permission denied, access denied
//   - Schema conflicts: Already exists (may or may not be an error depending on context)
var permanentPatterns = []string{
	"invalid",        // Invalid argument, invalid input, invalid tenant ID
	"permission",     // Permission denied
	"denied",         // Access denied
	"not allowed",    // Operation not allowed
	"constraint",     // Constraint violation, unique constraint
	"foreign key",    // Foreign key violation
	"duplicate",      // Duplicate key, duplicate entry
	"syntax",         // SQL syntax error
	"does not exist", // Table does not exist, column does not exist
	"not found",      // Resource not found (usually permanent)
	"unauthorized",   // Unauthorized access
	"authentication", // Authentication failed
	"invalid tenant", // Specific provisioner validation error
	"already active", // Tenant already provisioned
	"deprovisioned",  // Tenant is deprovisioned
}

// isRetryableError determines if a provisioning error is transient and worth retrying.
//
// Classification rules:
//  1. Nil errors are never retryable (no error to retry)
//  2. Context errors (Canceled, DeadlineExceeded) are never retryable
//  3. Errors matching permanent patterns are never retryable
//  4. Errors matching retryable patterns are retryable
//  5. Unknown errors default to non-retryable (fail-safe behavior)
//
// The function uses case-insensitive substring matching on error messages.
// For structured errors, it checks the full error chain via Error() string.
//
// Examples of retryable errors:
//   - "connection timeout waiting for lock"
//   - "unable to acquire advisory lock: timeout"
//   - "connection pool exhausted"
//   - "service temporarily unavailable"
//
// Examples of non-retryable errors:
//   - "invalid tenant ID format"
//   - "permission denied: insufficient privileges"
//   - "foreign key constraint violation"
//   - "duplicate key value violates unique constraint"
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Context errors are never retryable - the operation was explicitly cancelled
	// or the deadline was exceeded. Retrying won't help.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Convert error to lowercase string for case-insensitive matching.
	// This handles wrapped errors since Error() returns the full chain.
	errStr := strings.ToLower(err.Error())

	// Check permanent patterns first - these should never be retried
	for _, pattern := range permanentPatterns {
		if strings.Contains(errStr, pattern) {
			return false
		}
	}

	// Check for retryable patterns
	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Default to non-retryable for unknown errors.
	// This is a fail-safe: we'd rather fail fast on an unknown error
	// than waste time retrying something that will never succeed.
	return false
}
