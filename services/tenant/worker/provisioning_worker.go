// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ProvisioningWorker polls for tenants in PROVISIONING_PENDING status
// and triggers schema provisioning for them.
type ProvisioningWorker struct {
	repo         *persistence.Repository
	provisioner  provisioner.SchemaProvisioner
	pollInterval time.Duration
	logger       *slog.Logger
	done         chan struct{}
	wg           sync.WaitGroup // Tracks in-flight provisioning goroutines
}

// Errors returned by NewProvisioningWorker.
var (
	ErrNilRepository       = errors.New("repository cannot be nil")
	ErrNilProvisioner      = errors.New("provisioner cannot be nil")
	ErrNilLogger           = errors.New("logger cannot be nil")
	ErrInvalidPollInterval = errors.New("pollInterval must be greater than zero")
)

// NewProvisioningWorker creates a new ProvisioningWorker.
// All dependencies (repo, provisioner, logger) must be non-nil.
// pollInterval must be greater than zero.
func NewProvisioningWorker(
	repo *persistence.Repository,
	provisioner provisioner.SchemaProvisioner,
	pollInterval time.Duration,
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
	if pollInterval <= 0 {
		return nil, ErrInvalidPollInterval
	}

	return &ProvisioningWorker{
		repo:         repo,
		provisioner:  provisioner,
		pollInterval: pollInterval,
		logger:       logger,
		done:         make(chan struct{}),
	}, nil
}

// Start begins the polling loop to process pending tenant provisioning.
// It runs until ctx is cancelled or Stop() is called.
// The method blocks and should be run in a separate goroutine.
func (w *ProvisioningWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	w.logger.Info("provisioning worker started", "pollInterval", w.pollInterval)

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

// processPendingTenants queries for tenants in PROVISIONING_PENDING status
// and triggers provisioning for each one using optimistic locking.
func (w *ProvisioningWorker) processPendingTenants(ctx context.Context) {
	w.logger.Debug("checking for pending tenants to provision")

	// Fetch up to 10 pending tenants
	tenants, err := w.repo.ListByStatus(ctx, domain.StatusProvisioningPending, 10)
	if err != nil {
		w.logger.Error("failed to list pending tenants", "error", err)
		return
	}

	if len(tenants) == 0 {
		w.logger.Debug("no pending tenants found")
		return
	}

	w.logger.Info("found pending tenants", "count", len(tenants))

	// Process each tenant with optimistic locking
	for _, tenant := range tenants {
		// Attempt to claim the tenant by updating its status to PROVISIONING
		_, err := w.repo.UpdateStatus(ctx, tenant.ID, domain.StatusProvisioning, tenant.Version)
		if err != nil {
			// Version conflict or other error - log and continue to next tenant
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
}

// provisionTenantWithRetry provisions a tenant's schema with retry logic.
// It includes panic recovery to prevent crashes and proper goroutine lifecycle management.
// This is a stub implementation - actual retry logic and provisioning will be completed in task 71.
func (w *ProvisioningWorker) provisionTenantWithRetry(_ context.Context, tenantID tenant.TenantID) {
	// Ensure we decrement the WaitGroup when this goroutine completes
	defer w.wg.Done()

	// Panic recovery to prevent a single tenant provisioning failure from crashing the worker
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("panic during tenant provisioning",
				"tenant_id", tenantID,
				"panic", r)
		}
	}()

	w.logger.Info("provisioning tenant (stub)", "tenant_id", tenantID)

	// TODO(task-71): Implement actual provisioning logic with retry mechanism
	// This stub will be expanded to:
	// 1. Call w.provisioner.ProvisionSchema(ctx, tenantID) with the context parameter
	// 2. Implement exponential backoff retry logic
	// 3. Update tenant status to ACTIVE on success
	// 4. Update tenant status to PROVISIONING_FAILED on final failure
	// 5. Store error details in tenant.ErrorMessage field
}
