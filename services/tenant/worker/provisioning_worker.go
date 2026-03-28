// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/observability"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// PostProvisioningHook is called after schema provisioning succeeds but before
// marking the tenant as active. Hooks are non-blocking - errors are logged but
// do not prevent tenant activation. This allows for best-effort initialization
// of tenant-specific resources (e.g., default internal accounts).
type PostProvisioningHook func(ctx context.Context, tenantID tenant.TenantID) error

// ProvisioningWorker polls for tenants in PROVISIONING_PENDING status
// and triggers schema provisioning for them.
type ProvisioningWorker struct {
	repo                  *persistence.Repository
	provisioner           provisioner.SchemaProvisioner
	alertManager          *AlertManager
	postProvisioningHooks []namedHook
	pollInterval          time.Duration
	alertInterval         time.Duration
	alertThreshold        time.Duration
	recoveryThreshold     time.Duration
	maxRetries            int
	retryBaseDelay        time.Duration
	retryMaxDelay         time.Duration
	maxConcurrent         int
	logger                *slog.Logger
	done                  chan struct{}
	wg                    sync.WaitGroup // Tracks in-flight provisioning goroutines
	stopping              atomic.Bool    // Prevents new work during shutdown
	stoppingMu            sync.Mutex     // Guards stopping check + wg.Add to prevent race with wg.Wait
}

// namedHook wraps a hook with its name for logging.
type namedHook struct {
	name string
	hook PostProvisioningHook
}

// Errors returned by NewProvisioningWorker and provisioning operations.
var (
	ErrNilRepository        = errors.New("repository cannot be nil")
	ErrNilProvisioner       = errors.New("provisioner cannot be nil")
	ErrNilLogger            = errors.New("logger cannot be nil")
	ErrInvalidPollInterval  = errors.New("pollInterval must be greater than zero")
	ErrPanicDuringProvision = errors.New("panic during provisioning")
	ErrHookPanic            = errors.New("post-provisioning hook panicked")
)

// Config holds configuration for worker behavior.
type Config struct {
	PollInterval      time.Duration
	AlertInterval     time.Duration // Interval for checking failed provisioning alerts
	AlertThreshold    time.Duration // Age threshold for failed tenant alerting (default: 1 hour)
	RecoveryThreshold time.Duration // Age threshold for recovering stuck PROVISIONING tenants (default: 5 minutes)
	MaxRetries        int
	RetryBaseDelay    time.Duration
	RetryMaxDelay     time.Duration
	MaxConcurrent     int
}

// NewProvisioningWorker creates a new ProvisioningWorker.
// All dependencies (repo, provisioner, logger) must be non-nil.
// config.PollInterval must be greater than zero.
func NewProvisioningWorker(
	repo *persistence.Repository,
	provisioner provisioner.SchemaProvisioner,
	config Config,
	logger *slog.Logger,
) (*ProvisioningWorker, error) {
	if repo == nil {
		return nil, ErrNilRepository
	}
	if provisioner == nil {
		return nil, ErrNilProvisioner
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if config.PollInterval <= 0 {
		return nil, ErrInvalidPollInterval
	}

	resolved := applyConfigDefaults(config)

	return &ProvisioningWorker{
		repo:              repo,
		provisioner:       provisioner,
		alertManager:      NewAlertManager(repo, logger),
		pollInterval:      config.PollInterval,
		alertInterval:     resolved.AlertInterval,
		alertThreshold:    resolved.AlertThreshold,
		recoveryThreshold: resolved.RecoveryThreshold,
		maxRetries:        resolved.MaxRetries,
		retryBaseDelay:    resolved.RetryBaseDelay,
		retryMaxDelay:     resolved.RetryMaxDelay,
		maxConcurrent:     resolved.MaxConcurrent,
		logger:            logger,
		done:              make(chan struct{}),
	}, nil
}

// applyConfigDefaults fills zero-valued config fields with sensible defaults.
func applyConfigDefaults(config Config) Config {
	if config.AlertInterval <= 0 {
		config.AlertInterval = 15 * time.Minute
	}
	if config.AlertThreshold <= 0 {
		config.AlertThreshold = 1 * time.Hour
	}
	// Recovery threshold: time a tenant can be in PROVISIONING status before being
	// considered stuck and eligible for recovery on worker startup.
	if config.RecoveryThreshold <= 0 {
		config.RecoveryThreshold = 5 * time.Minute
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 5
	}
	if config.RetryBaseDelay <= 0 {
		config.RetryBaseDelay = 2 * time.Second
	}
	if config.RetryMaxDelay <= 0 {
		config.RetryMaxDelay = defaults.DefaultRPCTimeout
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 10
	}
	return config
}

// Start begins the polling loop to process pending tenant provisioning.
// It runs until ctx is cancelled or Stop() is called.
// The method blocks and should be run in a separate goroutine.
//
// On startup, it recovers any tenants that were stuck in PROVISIONING status
// from a previous worker crash. This ensures they get re-queued for provisioning.
func (w *ProvisioningWorker) Start(ctx context.Context) {
	// Recover any tenants stuck in PROVISIONING status from previous worker crash.
	// This is best-effort - we log errors but continue starting the worker.
	recoveredCount, err := w.RecoverStuckTenants(ctx, w.recoveryThreshold)
	if err != nil {
		w.logger.Error("failed to recover stuck tenants during startup", "error", err)
	} else if recoveredCount > 0 {
		w.logger.Info("startup recovery completed", "recovered_count", recoveredCount)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	alertTicker := time.NewTicker(w.alertInterval)
	defer alertTicker.Stop()

	w.logger.Info("provisioning worker started",
		"pollInterval", w.pollInterval,
		"alertInterval", w.alertInterval,
		"recoveryThreshold", w.recoveryThreshold)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("provisioning worker stopped: context cancelled")
			return
		case <-w.done:
			w.logger.Info("provisioning worker stopped: explicit shutdown")
			return
		case <-ticker.C:
			w.processPendingTenants(ctx)
		case <-alertTicker.C:
			w.checkFailedProvisioningAlerts(ctx)
		}
	}
}

// Stop signals the worker to shut down gracefully.
// It waits for all in-flight provisioning goroutines to complete.
// It is safe to call Stop multiple times.
func (w *ProvisioningWorker) Stop() {
	// Set stopping flag first to prevent new wg.Add() calls.
	// This must happen before closing the done channel to avoid a race
	// where processPendingTenants() is mid-execution and tries to add
	// new work while we're waiting.
	w.stopping.Store(true)

	select {
	case <-w.done:
		// Already closed
	default:
		close(w.done)
	}

	// Barrier: acquire and release stoppingMu so that any in-flight
	// processPendingTenants holding the lock for stopping check + wg.Add
	// must complete before we proceed to wg.Wait.
	w.stoppingMu.Lock()
	//nolint:staticcheck,gocritic // SA2001: intentional empty critical section used as barrier
	w.stoppingMu.Unlock()

	// Wait for all in-flight provisioning goroutines to complete
	w.logger.Info("waiting for in-flight provisioning to complete")
	w.wg.Wait()
	w.logger.Info("all provisioning goroutines completed")
}

// RegisterPostProvisioningHook adds a hook to be called after schema provisioning succeeds.
// Hooks are executed in registration order and are non-blocking - errors are logged
// but do not prevent tenant activation. Use this for best-effort initialization like
// creating default internal accounts.
//
// The name parameter is used for logging to identify which hook succeeded or failed.
func (w *ProvisioningWorker) RegisterPostProvisioningHook(name string, hook PostProvisioningHook) {
	w.postProvisioningHooks = append(w.postProvisioningHooks, namedHook{
		name: name,
		hook: hook,
	})
	w.logger.Info("registered post-provisioning hook", "name", name)
}

// executePostProvisioningHooks runs all registered hooks sequentially.
// Errors are logged but do not stop execution of subsequent hooks.
// Returns the count of hooks that succeeded.
func (w *ProvisioningWorker) executePostProvisioningHooks(ctx context.Context, tenantID tenant.TenantID) int {
	if len(w.postProvisioningHooks) == 0 {
		return 0
	}

	w.logger.Debug("executing post-provisioning hooks",
		"tenant_id", tenantID,
		"hook_count", len(w.postProvisioningHooks))

	succeeded := 0
	for _, nh := range w.postProvisioningHooks {
		if err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%w: %v", ErrHookPanic, r)
				}
			}()
			return nh.hook(ctx, tenantID)
		}(); err != nil {
			// Log error but continue - hooks are non-blocking
			w.logger.Warn("post-provisioning hook failed",
				"tenant_id", tenantID,
				"hook_name", nh.name,
				"error", err)
		} else {
			w.logger.Debug("post-provisioning hook succeeded",
				"tenant_id", tenantID,
				"hook_name", nh.name)
			succeeded++
		}
	}

	w.logger.Info("post-provisioning hooks completed",
		"tenant_id", tenantID,
		"succeeded", succeeded,
		"total", len(w.postProvisioningHooks))

	return succeeded
}

// checkFailedProvisioningAlerts checks for persistent provisioning failures
// and logs alerts for external alerting system integration.
func (w *ProvisioningWorker) checkFailedProvisioningAlerts(ctx context.Context) {
	w.logger.Debug("checking for persistent provisioning failures")

	// Check for tenants that have been in provisioning_failed for more than the configured threshold.
	// This threshold prevents alerting on transient failures that may self-recover.
	if err := w.alertManager.CheckFailedProvisioningAlerts(ctx, w.alertThreshold); err != nil {
		w.logger.Error("failed to check provisioning alerts", "error", err)
	}
}

// processPendingTenants queries for tenants in PROVISIONING_PENDING status
// and triggers provisioning for each one using optimistic locking.
func (w *ProvisioningWorker) processPendingTenants(ctx context.Context) {
	w.logger.Debug("checking for pending tenants to provision")

	// Fetch up to maxConcurrent pending tenants
	tenants, err := w.repo.ListByStatus(ctx, domain.StatusProvisioningPending, w.maxConcurrent)
	if err != nil {
		w.logger.Error("failed to list pending tenants", "error", err)
		return
	}

	if len(tenants) == 0 {
		w.logger.Debug("no pending tenants found")
		observability.SetProvisioningQueueDepth(0)
		return
	}

	w.logger.Info("found pending tenants", "count", len(tenants))
	observability.SetProvisioningQueueDepth(len(tenants))

	// Process each tenant with optimistic locking
	for _, tenant := range tenants {
		w.claimAndProvisionTenant(ctx, tenant)
	}
}

// claimAndProvisionTenant attempts to claim a pending tenant via optimistic locking
// and spawns a background goroutine for provisioning on success.
func (w *ProvisioningWorker) claimAndProvisionTenant(ctx context.Context, t *domain.Tenant) {
	// Attempt to claim the tenant by updating its status to PROVISIONING
	_, err := w.repo.UpdateStatus(ctx, t.ID, domain.StatusProvisioning, t.Version)
	if err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			w.logger.Debug("tenant already claimed by another worker",
				"tenant_id", t.ID,
				"expected_version", t.Version)
			return
		}
		w.logger.Warn("failed to claim tenant for provisioning",
			"tenant_id", t.ID,
			"version", t.Version,
			"error", err)
		return
	}

	w.logger.Info("claimed tenant for provisioning",
		"tenant_id", t.ID,
		"schema", t.SchemaName())

	// Atomically check stopping + wg.Add under stoppingMu to prevent
	// a race where Stop() calls wg.Wait() between our check and Add.
	w.stoppingMu.Lock()
	if w.stopping.Load() {
		w.stoppingMu.Unlock()
		w.logger.Warn("not spawning provisioning goroutine - worker is stopping",
			"tenant_id", t.ID)
		return
	}
	w.wg.Add(1)
	w.stoppingMu.Unlock()

	// Spawn provisioning in background with detached context
	go w.provisionTenantWithRetry(context.WithoutCancel(ctx), t.ID)
}

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
			w.markTenantAsActive(ctx, tenantID, attempts)
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
// Before marking as active, it executes any registered post-provisioning hooks
// (e.g., creating default internal accounts). Hook failures are logged but
// do not prevent tenant activation.
func (w *ProvisioningWorker) markTenantAsActive(ctx context.Context, tenantID tenant.TenantID, attempt int) {
	w.logger.Info("provisioning succeeded",
		"tenant_id", tenantID,
		"attempt", attempt)

	// Execute post-provisioning hooks (non-blocking)
	// These hooks can initialize tenant-specific resources like default internal accounts.
	// Failures are logged but do not prevent tenant activation.
	w.executePostProvisioningHooks(ctx, tenantID)

	tenant, getErr := w.repo.GetByID(ctx, tenantID)
	if getErr != nil {
		w.logger.Error("failed to get tenant after successful provisioning",
			"tenant_id", tenantID,
			"error", getErr)
		return
	}

	_, updateErr := w.repo.UpdateStatus(ctx, tenantID, domain.StatusActive, tenant.Version)
	if updateErr != nil {
		w.logger.Error("failed to mark tenant active",
			"tenant_id", tenantID,
			"version", tenant.Version,
			"error", updateErr)
	}
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

// RecoverStuckTenants resets tenants that have been stuck in PROVISIONING status
// for longer than the specified threshold back to PROVISIONING_PENDING status.
// This allows them to be picked up and re-provisioned on the next polling cycle.
//
// This method is designed to handle crash recovery scenarios where the worker
// stopped before completing provisioning. It uses optimistic locking to prevent
// race conditions with tenants that are actively being provisioned.
//
// Returns the count of tenants successfully recovered and any query error.
// Version conflicts during recovery are logged but not treated as errors since
// they indicate the tenant is likely being actively provisioned.
func (w *ProvisioningWorker) RecoverStuckTenants(ctx context.Context, staleThreshold time.Duration) (int, error) {
	cutoff := time.Now().Add(-staleThreshold)

	// Query for tenants stuck in PROVISIONING status older than threshold
	stuckTenants, err := w.repo.ListByStatusOlderThan(ctx, domain.StatusProvisioning, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to find stale provisioning tenants: %w", err)
	}

	if len(stuckTenants) == 0 {
		w.logger.Debug("no stuck tenants found for recovery")
		return 0, nil
	}

	w.logger.Info("found stuck tenants for recovery",
		"count", len(stuckTenants),
		"stale_threshold", staleThreshold)

	recovered := 0
	for _, t := range stuckTenants {
		// Attempt to reset tenant to PROVISIONING_PENDING
		_, updateErr := w.repo.UpdateStatus(ctx, t.ID, domain.StatusProvisioningPending, t.Version)
		if updateErr != nil {
			// Version conflict is expected if tenant was concurrently modified
			// (e.g., actually being provisioned right now)
			if errors.Is(updateErr, persistence.ErrVersionConflict) {
				w.logger.Debug("skipping recovery for concurrently modified tenant",
					"tenant_id", t.ID,
					"version", t.Version)
				continue
			}
			// Log other errors at warn level but continue with other tenants
			w.logger.Warn("failed to recover stale tenant",
				"tenant_id", t.ID,
				"version", t.Version,
				"error", updateErr)
			continue
		}

		recovered++
		w.logger.Info("recovered stale tenant from PROVISIONING to PROVISIONING_PENDING",
			"tenant_id", t.ID,
			"stale_threshold", staleThreshold)
	}

	if recovered > 0 {
		w.logger.Info("stuck tenant recovery completed",
			"recovered", recovered,
			"total_stuck", len(stuckTenants))
	}

	return recovered, nil
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
