// Package provisioner - deprovision, purge, and status accessor operations.
package provisioner

import (
	"context"
	"errors"
	"fmt"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// DeprovisionSchemas marks the tenant schemas as deprovisioned (soft delete).
// The actual schema data remains in the database until explicitly purged.
func (p *PostgresProvisioner) DeprovisionSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logger := p.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("starting schema deprovisioning")

	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil {
		logger.Error("failed to get provisioning status", "error", err)
		return err
	}

	// Idempotent: already deprovisioned
	if status.State == StateDeprovisioned {
		logger.Info("schema already deprovisioned, skipping")
		return nil
	}

	// Mark as deprovisioned
	now := timeNow()
	status.State = StateDeprovisioned
	status.DeprovisionedAt = &now
	status.UpdatedAt = now

	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Error("failed to save deprovisioned status", "error", err)
		return fmt.Errorf("%w: %v", ErrDeprovisioningFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}

	logger.Info("schema deprovisioning completed")
	return nil
}

// PurgeSchemas permanently drops the org_{tenant_id} schema and all data.
// This is a DESTRUCTIVE operation that cannot be undone.
//
// Prerequisites:
//   - Tenant must be in 'deprovisioned' state
//   - Data retention period must have elapsed
func (p *PostgresProvisioner) PurgeSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logger := p.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("starting schema purge")

	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil {
		logger.Error("failed to get provisioning status", "error", err)
		return err
	}

	// Must be deprovisioned first
	if status.State != StateDeprovisioned {
		logger.Warn("cannot purge: tenant not deprovisioned", "current_state", status.State)
		return ErrNotDeprovisioned
	}

	// Check retention period
	if status.DeprovisionedAt != nil && p.config.DataRetentionPeriod > 0 {
		retentionEnd := status.DeprovisionedAt.Add(p.config.DataRetentionPeriod)
		if timeNow().Before(retentionEnd) {
			logger.Warn("cannot purge: retention period not elapsed",
				"deprovisioned_at", status.DeprovisionedAt,
				"retention_ends", retentionEnd)
			return ErrRetentionPeriodNotElapsed
		}
	}

	// Drop the schema from ALL service databases
	schemaName := tenantID.SchemaName()
	if err := p.dropSchemaInAllDBs(ctx, schemaName); err != nil {
		logger.Error("failed to drop schemas from service databases", "error", err)
		return fmt.Errorf("%w: %v", ErrDeprovisioningFailed, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}
	logger.Debug("schemas dropped from all service databases")

	// Remove the provisioning record
	if err := p.deleteProvisioningStatus(ctx, tenantID); err != nil {
		logger.Error("failed to delete provisioning status", "error", err)
		return fmt.Errorf("delete provisioning status: %w", err)
	}

	logger.Info("schema purge completed")
	return nil
}

// GetProvisioningStatus retrieves the current provisioning state for a tenant.
func (p *PostgresProvisioner) GetProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getProvisioningStatusLocked(ctx, tenantID)
}

// GetRequiredSchemas returns the list of service names that require schema provisioning.
func (p *PostgresProvisioner) GetRequiredSchemas() []string {
	schemas := make([]string, 0, len(p.config.Services))
	for _, svc := range p.config.Services {
		schemas = append(schemas, svc.Name)
	}
	return schemas
}

// InitializeProvisioningStatus creates an initial provisioning_status record with 'pending' state.
// This is called during tenant creation to set up tracking records before async provisioning begins.
// Idempotent: If a status record already exists, this is a no-op.
func (p *PostgresProvisioner) InitializeProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	logger := p.logger.With("tenant_id", tenantID.String())

	// Check if status already exists (idempotency)
	_, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err == nil {
		logger.Debug("provisioning status already exists, skipping initialization")
		return nil // Already exists, no-op
	}
	if !errors.Is(err, ErrProvisioningStatusNotFound) {
		logger.Error("failed to check existing provisioning status", "error", err)
		return fmt.Errorf("check existing status: %w", err)
	}

	// Create initial status with pending state
	now := timeNow()
	status := &ProvisioningStatus{
		TenantID:  tenantID,
		State:     StatePending,
		Services:  p.createInitialServiceStatuses(tenantID),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Error("failed to save initial provisioning status", "error", err)
		return fmt.Errorf("save provisioning status: %w", err)
	}

	logger.Debug("provisioning status initialized",
		"state", status.State,
		"services_count", len(status.Services))

	return nil
}

// Close closes all service database connections created by this provisioner.
// This should be called during graceful shutdown to release database resources.
//
// Note: platformDB is NOT closed here because it is an injected dependency.
func (p *PostgresProvisioner) Close() error {
	var errs []error
	for _, svc := range p.config.Services {
		db, ok := p.serviceDbs[svc.Name]
		if !ok {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			p.logger.Warn("failed to get underlying DB for close", "service", svc.Name, "error", err)
			errs = append(errs, fmt.Errorf("%s: get DB: %w", svc.Name, err))
			continue
		}
		if err := sqlDB.Close(); err != nil {
			p.logger.Warn("failed to close database connection", "service", svc.Name, "error", err)
			errs = append(errs, fmt.Errorf("%s: close: %w", svc.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrCloseConnections, errors.Join(errs...)) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}
	return nil
}

// GetCircuitBreakerStates returns the current state of all service circuit breakers.
func (p *PostgresProvisioner) GetCircuitBreakerStates() []CircuitBreakerState {
	return p.circuitBreakers.GetAllCircuitBreakerStates()
}

// GetCircuitBreakerState returns the current state of the circuit breaker for a specific service.
// Returns nil if the service has never been accessed (no circuit breaker exists).
func (p *PostgresProvisioner) GetCircuitBreakerState(serviceName string) *CircuitBreakerState {
	return p.circuitBreakers.GetCircuitBreakerState(serviceName)
}

// hasNewServices returns true if the config contains services not yet in the stored status.
func (p *PostgresProvisioner) hasNewServices(status *ProvisioningStatus) bool {
	existing := make(map[string]bool, len(status.Services))
	for _, svc := range status.Services {
		existing[svc.ServiceName] = true
	}
	for _, svc := range p.config.Services {
		if !existing[svc.Name] {
			return true
		}
	}
	return false
}

// reconcileServiceStatuses appends ServiceSchemaStatus entries for any config
// services not already present in the stored status, matched by service name.
func (p *PostgresProvisioner) reconcileServiceStatuses(status *ProvisioningStatus, tenantID tenant.TenantID) {
	existing := make(map[string]bool, len(status.Services))
	for _, svc := range status.Services {
		existing[svc.ServiceName] = true
	}
	schemaName := tenantID.SchemaName()
	for _, svc := range p.config.Services {
		if !existing[svc.Name] {
			status.Services = append(status.Services, ServiceSchemaStatus{
				ServiceName: svc.Name,
				SchemaName:  schemaName,
				State:       ServiceStatePending,
			})
		}
	}
}
