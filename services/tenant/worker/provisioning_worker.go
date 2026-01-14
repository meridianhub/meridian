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
// of tenant-specific resources (e.g., default internal bank accounts).
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
	maxRetries            int
	retryBaseDelay        time.Duration
	retryMaxDelay         time.Duration
	maxConcurrent         int
	logger                *slog.Logger
	done                  chan struct{}
	wg                    sync.WaitGroup // Tracks in-flight provisioning goroutines
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
)

// Config holds configuration for worker behavior.
type Config struct {
	PollInterval   time.Duration
	AlertInterval  time.Duration // Interval for checking failed provisioning alerts
	AlertThreshold time.Duration // Age threshold for failed tenant alerting (default: 1 hour)
	MaxRetries     int
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	MaxConcurrent  int
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

	// Default alert interval to 15 minutes if not specified
	alertInterval := config.AlertInterval
	if alertInterval <= 0 {
		alertInterval = 15 * time.Minute
	}

	// Default alert threshold to 1 hour if not specified
	alertThreshold := config.AlertThreshold
	if alertThreshold <= 0 {
		alertThreshold = 1 * time.Hour
	}

	// Default max retries to 5 if not specified
	maxRetries := config.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}

	// Default retry base delay to 2 seconds if not specified
	retryBaseDelay := config.RetryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = 2 * time.Second
	}

	// Default retry max delay to RPC timeout if not specified
	retryMaxDelay := config.RetryMaxDelay
	if retryMaxDelay <= 0 {
		retryMaxDelay = defaults.DefaultRPCTimeout
	}

	// Default max concurrent to 10 if not specified
	maxConcurrent := config.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}

	return &ProvisioningWorker{
		repo:           repo,
		provisioner:    provisioner,
		alertManager:   NewAlertManager(repo, logger),
		pollInterval:   config.PollInterval,
		alertInterval:  alertInterval,
		alertThreshold: alertThreshold,
		maxRetries:     maxRetries,
		retryBaseDelay: retryBaseDelay,
		retryMaxDelay:  retryMaxDelay,
		maxConcurrent:  maxConcurrent,
		logger:         logger,
		done:           make(chan struct{}),
	}, nil
}

// Start begins the polling loop to process pending tenant provisioning.
// It runs until ctx is cancelled or Stop() is called.
// The method blocks and should be run in a separate goroutine.
func (w *ProvisioningWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	alertTicker := time.NewTicker(w.alertInterval)
	defer alertTicker.Stop()

	w.logger.Info("provisioning worker started",
		"pollInterval", w.pollInterval,
		"alertInterval", w.alertInterval)

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
	select {
	case <-w.done:
		// Already closed
	default:
		close(w.done)
	}

	// Wait for all in-flight provisioning goroutines to complete
	w.logger.Info("waiting for in-flight provisioning to complete")
	w.wg.Wait()
	w.logger.Info("all provisioning goroutines completed")
}

// RegisterPostProvisioningHook adds a hook to be called after schema provisioning succeeds.
// Hooks are executed in registration order and are non-blocking - errors are logged
// but do not prevent tenant activation. Use this for best-effort initialization like
// creating default internal bank accounts.
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
		if err := nh.hook(ctx, tenantID); err != nil {
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

	// Record queue depth before processing
	observability.SetProvisioningQueueDepth(len(tenants))

	// Process each tenant with optimistic locking
	for _, tenant := range tenants {
		// Attempt to claim the tenant by updating its status to PROVISIONING
		_, err := w.repo.UpdateStatus(ctx, tenant.ID, domain.StatusProvisioning, tenant.Version)
		if err != nil {
			// Check if this is a version conflict (another worker claimed it first)
			if errors.Is(err, persistence.ErrVersionConflict) {
				// Expected during concurrent operation - debug level logging
				w.logger.Debug("tenant already claimed by another worker",
					"tenant_id", tenant.ID,
					"expected_version", tenant.Version)
				continue
			}
			// Unexpected error - warn level logging
			w.logger.Warn("failed to claim tenant for provisioning",
				"tenant_id", tenant.ID,
				"version", tenant.Version,
				"error", err)
			continue
		}

		// Successfully claimed - spawn goroutine to provision
		w.logger.Info("claimed tenant for provisioning",
			"tenant_id", tenant.ID,
			"schema", tenant.SchemaName())

		// Track the goroutine in the WaitGroup
		w.wg.Add(1)

		// Spawn provisioning in background with detached context
		// We use context.WithoutCancel to prevent parent cancellation from stopping provisioning
		go w.provisionTenantWithRetry(context.WithoutCancel(ctx), tenant.ID)
	}

	// Note: We intentionally do NOT reset queue depth to 0 here.
	// The next poll cycle will set the accurate count of PROVISIONING_PENDING tenants.
	// Resetting to 0 could cause misleading dashboard values if this function
	// is called again before all goroutines complete.
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
// (e.g., creating default internal bank accounts). Hook failures are logged but
// do not prevent tenant activation.
func (w *ProvisioningWorker) markTenantAsActive(ctx context.Context, tenantID tenant.TenantID, attempt int) {
	w.logger.Info("provisioning succeeded",
		"tenant_id", tenantID,
		"attempt", attempt)

	// Execute post-provisioning hooks (non-blocking)
	// These hooks can initialize tenant-specific resources like default internal bank accounts.
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
// TODO: When service-specific provisioning is implemented, call observability.IncrementServiceFailure(serviceName)
// here based on which service (database, kafka, etc.) caused the failure. Currently, the provisioner
// returns generic errors without service attribution.
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
