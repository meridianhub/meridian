// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// marking the tenant as active. Hook failures are fatal - they prevent tenant
// activation and mark provisioning as failed. This ensures tenants are never
// activated with incomplete reference data (instruments, sagas, account types).
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
// On startup, it performs two best-effort passes before entering the polling loop:
//  1. Recover any tenants stuck in PROVISIONING status from a previous worker crash,
//     re-queuing them for provisioning.
//  2. Reconcile migrations against all active tenants, applying any new migration
//     files that were added since each tenant was originally provisioned. This
//     allows new schema additions (e.g. audit tables) to roll out to existing
//     tenants on the next deploy without requiring a manual gRPC trigger.
//
// Both passes log errors but never block the worker from starting - a failed
// reconciliation should not stop the worker from processing pending tenants.
func (w *ProvisioningWorker) Start(ctx context.Context) {
	// Recover any tenants stuck in PROVISIONING status from previous worker crash.
	// This is best-effort - we log errors but continue starting the worker.
	recoveredCount, err := w.RecoverStuckTenants(ctx, w.recoveryThreshold)
	if err != nil {
		w.logger.Error("failed to recover stuck tenants during startup", "error", err)
	} else if recoveredCount > 0 {
		w.logger.Info("startup recovery completed", "recovered_count", recoveredCount)
	}

	// Reconcile migrations across all active tenants. This applies new migration
	// files (e.g. the identity audit_log/audit_outbox tables) to schemas that
	// were provisioned before those migrations existed. Best-effort - per-tenant
	// errors are logged but do not prevent the worker from starting.
	w.reconcileMigrationsOnStartup(ctx)

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
// Hooks are executed in registration order and are fail-hard - any hook failure
// prevents tenant activation and marks provisioning as failed.
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
// Any hook failure is fatal - it stops execution and returns the error.
// This ensures tenants are never activated with incomplete reference data.
func (w *ProvisioningWorker) executePostProvisioningHooks(ctx context.Context, tenantID tenant.TenantID) error {
	if len(w.postProvisioningHooks) == 0 {
		return nil
	}

	w.logger.Debug("executing post-provisioning hooks",
		"tenant_id", tenantID,
		"hook_count", len(w.postProvisioningHooks))

	for _, nh := range w.postProvisioningHooks {
		if err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%w: %v", ErrHookPanic, r)
				}
			}()
			return nh.hook(ctx, tenantID)
		}(); err != nil {
			w.logger.Error("post-provisioning hook failed - aborting tenant activation",
				"tenant_id", tenantID,
				"hook_name", nh.name,
				"error", err)
			return fmt.Errorf("post-provisioning hook %q failed: %w", nh.name, err)
		}
		w.logger.Debug("post-provisioning hook succeeded",
			"tenant_id", tenantID,
			"hook_name", nh.name)
	}

	w.logger.Info("all post-provisioning hooks completed",
		"tenant_id", tenantID,
		"total", len(w.postProvisioningHooks))

	return nil
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

// reconcileMigrationsOnStartup invokes the provisioner's ReconcileMigrations against
// all active tenants. This applies any new migration files that were added since the
// tenant was originally provisioned.
//
// Best-effort: per-tenant errors are logged but never propagated. The method panics
// recovery so a misbehaving provisioner cannot prevent worker startup.
func (w *ProvisioningWorker) reconcileMigrationsOnStartup(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic during startup migration reconciliation",
				"panic", r)
		}
	}()

	w.logger.Info("starting migration reconciliation for active tenants")

	reconciledCount, errs := w.provisioner.ReconcileMigrations(ctx, nil)

	if len(errs) > 0 {
		w.logger.Warn("startup migration reconciliation completed with errors",
			"reconciled_count", reconciledCount,
			"error_count", len(errs),
			"errors", errs)
		return
	}

	w.logger.Info("startup migration reconciliation completed",
		"reconciled_count", reconciledCount)
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
