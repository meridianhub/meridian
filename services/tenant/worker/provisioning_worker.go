// Package worker implements background workers for tenant provisioning.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
)

// ProvisioningWorker polls for tenants in PROVISIONING_PENDING status
// and triggers schema provisioning for them.
type ProvisioningWorker struct {
	repo         *persistence.Repository
	provisioner  provisioner.SchemaProvisioner
	pollInterval time.Duration
	logger       *slog.Logger
	done         chan struct{}
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
// It is safe to call Stop multiple times.
func (w *ProvisioningWorker) Stop() {
	select {
	case <-w.done:
		// Already closed
	default:
		close(w.done)
	}
}

// processPendingTenants queries for tenants in PROVISIONING_PENDING status
// and triggers provisioning for each one.
// This is a stub implementation - will be completed in subtask 70.3.
func (w *ProvisioningWorker) processPendingTenants(_ context.Context) {
	w.logger.Debug("checking for pending tenants to provision")
	// TODO: implement in subtask 70.3
}
