// Package worker implements background workers for tenant provisioning.
package worker

import (
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
