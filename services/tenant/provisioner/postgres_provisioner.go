// Package provisioner provides PostgreSQL schema provisioning for multi-tenant isolation.
package provisioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/meridianhub/meridian/services/tenant/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/sony/gobreaker/v2"
	"gorm.io/gorm"
)

// errAlreadyProvisioned is an internal sentinel for idempotent provisioning.
// It's not a public error because it's not an actual error condition.
var errAlreadyProvisioned = errors.New("already provisioned")

// PostgresProvisioner implements SchemaProvisioner for PostgreSQL databases.
// It creates org_{tenant_id} schemas and applies service migrations using SQL files.
//
// In database-per-service architecture, schemas are created in each service's
// dedicated database rather than a single shared database.
//
// Thread-safe for concurrent use via mutex protection around status operations.
type PostgresProvisioner struct {
	// platformDB connects to meridian_platform for tenant_provisioning table.
	// This is the single source of truth for provisioning status tracking.
	platformDB *gorm.DB

	// serviceDbs holds database connections indexed by service name.
	// Each service has its own database where org_{tenant_id} schemas are created.
	serviceDbs map[string]*gorm.DB

	// circuitBreakers manages per-service circuit breakers to prevent repeated
	// provisioning attempts when a specific service database is consistently failing.
	circuitBreakers *ServiceCircuitBreakers

	config *Config
	logger *slog.Logger

	// mu protects concurrent access to provisioning operations for the same tenant.
	// This prevents race conditions when multiple goroutines attempt to provision
	// the same tenant simultaneously.
	mu sync.Mutex
}

// NewPostgresProvisioner creates a new PostgreSQL schema provisioner.
//
// Parameters:
//   - platformDB: Connection to meridian_platform database for tenant_provisioning table.
//   - config: Configuration with service definitions including their database URLs.
//
// The constructor establishes connections to all service databases defined in config.
// Each service database will have org_{tenant_id} schemas created during provisioning.
//
// Returns an error if the config is invalid or any database connection fails.
func NewPostgresProvisioner(platformDB *gorm.DB, config *Config) (*PostgresProvisioner, error) {
	if platformDB == nil {
		return nil, ErrNilPlatformDB
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	serviceDbs, err := openServiceConnections(config.Services)
	if err != nil {
		return nil, err
	}

	return &PostgresProvisioner{
		platformDB:      platformDB,
		serviceDbs:      serviceDbs,
		circuitBreakers: NewServiceCircuitBreakers(),
		config:          config,
		logger:          slog.Default().With("component", "provisioner"),
	}, nil
}

// ProvisionSchemas creates the org_{tenant_id} schema and applies all service migrations.
//
// In database-per-service architecture, the function:
//  1. Creates or updates the provisioning status record to 'in_progress' (in platform DB)
//  2. Creates the org_{tenant_id} schema in EACH service's database
//  3. For each configured service, applies migrations to the tenant schema in that service's database
//  4. Updates the provisioning status to 'active' on success or 'failed' on error
//
// # Idempotency Contract
//
// This method is safe to call multiple times for the same tenant. Calling ProvisionSchemas
// on an already-provisioned tenant is a no-op that returns nil (success).
//
// Thread-safety: Safe for concurrent calls. The mutex serializes provisioning for the
// same tenant, while different tenants can be provisioned in parallel.
func (p *PostgresProvisioner) ProvisionSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	startTime := timeNow()
	logger := p.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("starting schema provisioning", "services_count", len(p.config.Services))

	// Validate and prepare provisioning status.
	status, err := p.prepareProvisioningStatus(ctx, tenantID)
	if errors.Is(err, errAlreadyProvisioned) {
		logger.Info("schema already provisioned, skipping")
		return nil
	}
	if err != nil {
		logger.Error("failed to prepare provisioning status", "error", err)
		return err
	}

	// Apply timeout from config
	if p.config.ProvisioningTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.config.ProvisioningTimeout)
		defer cancel()
	}

	if err := p.createAndMigrateSchemas(ctx, tenantID, status, logger); err != nil {
		return err
	}

	// Verify expected tables exist before transitioning to active.
	// This catches partial provisioning where schema exists but migrations failed silently.
	if err := p.verifySchemaProvisioned(ctx, tenantID.SchemaName(), logger); err != nil {
		logger.Error("schema verification failed after migrations", "error", err)
		p.markProvisioningFailed(ctx, status, err.Error())
		return err
	}

	// Mark as active
	status.State = StateActive
	status.UpdatedAt = timeNow()
	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Error("failed to save final status", "error", err)
		return fmt.Errorf("save final status: %w", err)
	}

	// Run post-provisioning hooks (e.g., saga seeding).
	p.runPostProvisioningHooks(ctx, tenantID, logger)

	logger.Info("schema provisioning completed",
		"duration_ms", time.Since(startTime).Milliseconds(),
		"services_count", len(p.config.Services))
	return nil
}

// createAndMigrateSchemas creates org schemas in all service databases and applies migrations.
func (p *PostgresProvisioner) createAndMigrateSchemas(ctx context.Context, tenantID tenant.TenantID, status *ProvisioningStatus, logger *slog.Logger) error {
	// Check context before expensive operations
	if ctx.Err() != nil {
		logger.Warn("context cancelled before schema creation")
		p.markProvisioningFailed(ctx, status, "cancelled before schema creation")
		return ErrProvisioningTimeout
	}

	// Create the schema IN EACH SERVICE DATABASE.
	// IDEMPOTENCY: Uses CREATE SCHEMA IF NOT EXISTS (see createSchemaInDB).
	schemaName := tenantID.SchemaName()
	for _, svc := range p.config.Services {
		serviceDB := p.serviceDbs[svc.Name]
		if err := p.createSchemaInDB(ctx, serviceDB, schemaName); err != nil {
			logger.Error("failed to create schema in service database",
				"service", svc.Name,
				"error", err)
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("create schema in %s: %v", svc.Name, err))
			return fmt.Errorf("%w: %s: %v", ErrSchemaCreationFailed, svc.Name, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}
		logger.Debug("schema created in service database", "service", svc.Name)
	}

	// Apply migrations for each service
	if err := p.provisionAllServices(ctx, status, schemaName); err != nil {
		logger.Error("failed to provision services", "error", err)
		return err
	}

	return nil
}

// prepareProvisioningStatus validates existing status and prepares for provisioning.
//
// IDEMPOTENCY: This is the primary idempotency gate for ProvisionSchemas:
//   - StateActive: Returns errAlreadyProvisioned (caller treats as success/no-op)
//   - StateInProgress: Returns ErrProvisioningInProgress (prevents concurrent provisioning)
//   - StateDeprovisioned: Returns ErrAlreadyDeprovisioned (terminal state, cannot re-provision)
//   - StatePending/StateFailed: Proceeds with provisioning (retry is allowed)
func (p *PostgresProvisioner) prepareProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil && !errors.Is(err, ErrProvisioningStatusNotFound) {
		return nil, fmt.Errorf("get provisioning status: %w", err)
	}

	// Check existing state
	if status != nil {
		switch status.State {
		case StateActive:
			// Check if new services were added to the config since last provisioning.
			// Compare by service name set to handle reordering.
			if !p.hasNewServices(status) {
				return nil, errAlreadyProvisioned // Idempotent: already provisioned
			}
			// New services added - fall through to re-provision
		case StateInProgress:
			return nil, ErrProvisioningInProgress
		case StateDeprovisioned:
			return nil, ErrAlreadyDeprovisioned
		case StatePending, StateFailed:
			// Can proceed with provisioning/retry
		}
	}

	// Create or update status to in_progress
	now := timeNow()
	if status == nil {
		status = &ProvisioningStatus{
			TenantID:  tenantID,
			State:     StateInProgress,
			Services:  p.createInitialServiceStatuses(tenantID),
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else {
		status.State = StateInProgress
		status.UpdatedAt = now
		status.ErrorMessage = ""
		// Reconcile services list with current config. Appends entries for any
		// services in DefaultConfig() that aren't in the stored status, matched
		// by service name rather than index position.
		p.reconcileServiceStatuses(status, tenantID)
	}

	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		return nil, fmt.Errorf("save provisioning status: %w", err)
	}

	return status, nil
}

// provisionAllServices applies migrations for each configured service.
// Each service's migrations are applied to that service's dedicated database.
// Circuit breaker protection is applied per-service to prevent repeated failures.
func (p *PostgresProvisioner) provisionAllServices(ctx context.Context, status *ProvisioningStatus, schemaName string) error {
	logger := p.logger.With("tenant_id", status.TenantID.String(), "schema", schemaName)

	var skippedServices []string

	for i, svc := range p.config.Services {
		if ctx.Err() != nil {
			logger.Warn("provisioning timeout", "service", svc.Name)
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("timeout during %s migrations", svc.Name))
			return ErrProvisioningTimeout
		}

		// Skip services that are already successfully provisioned
		if i < len(status.Services) && status.Services[i].State == ServiceStateMigrated {
			logger.Debug("service already provisioned, skipping", "service", svc.Name)
			continue
		}

		skipped, err := p.provisionSingleService(ctx, status, i, svc, schemaName, logger)
		if err != nil {
			return err
		}
		if skipped {
			skippedServices = append(skippedServices, svc.Name)
		}
	}

	// If any services were skipped due to circuit breaker, mark as partial failure
	if len(skippedServices) > 0 {
		errMsg := fmt.Sprintf("services skipped due to circuit breaker: %v", skippedServices)
		logger.Warn("partial provisioning completed", "skipped_services", skippedServices)
		p.markProvisioningFailed(ctx, status, errMsg)
		return fmt.Errorf("%w: %s", ErrCircuitBreakerOpen, strings.Join(skippedServices, ", "))
	}

	return nil
}

// provisionSingleService provisions one service: resolves DB, applies migrations via circuit breaker.
// Returns (true, nil) if the service was skipped due to circuit breaker, (false, nil) on success,
// or (false, error) on fatal failure.
func (p *PostgresProvisioner) provisionSingleService(ctx context.Context, status *ProvisioningStatus, idx int, svc ServiceConfig, schemaName string, logger *slog.Logger) (bool, error) {
	// Get the database connection for this service
	serviceDB, ok := p.serviceDbs[svc.Name]
	if !ok {
		logger.Error("service database not found", "service", svc.Name)
		observability.IncrementServiceFailure(svc.Name)
		p.markProvisioningFailed(ctx, status, fmt.Sprintf("no database connection for service: %s", svc.Name))
		return false, fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name)
	}

	logger.Debug("provisioning service",
		"service", svc.Name,
		"migration_path", svc.MigrationPath,
		"database_url", maskDatabaseURL(svc.DatabaseURL))

	// Update service status to created
	status.Services[idx].State = ServiceStateCreated
	status.UpdatedAt = timeNow()
	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Warn("failed to save intermediate status", "error", err, "service", svc.Name)
	}

	// Apply migrations through circuit breaker
	breaker := p.circuitBreakers.GetBreaker(svc.Name)
	version, err := p.applyMigrationsWithCircuitBreaker(ctx, breaker, serviceDB, schemaName, svc, logger)

	// Handle circuit breaker specific errors
	if skipped := p.handleCircuitBreakerError(err, status, idx, svc, logger); skipped {
		return true, nil
	}

	if err != nil {
		logger.Error("service migration failed", "service", svc.Name, "error", err)
		observability.IncrementServiceFailure(svc.Name)
		status.Services[idx].State = ServiceStateFailed
		status.Services[idx].ErrorMessage = err.Error()
		p.markProvisioningFailed(ctx, status, fmt.Sprintf("%s migrations failed: %v", svc.Name, err))
		return false, fmt.Errorf("%w: %s: %v", ErrMigrationFailed, svc.Name, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}

	logger.Debug("service migration completed", "service", svc.Name, "version", version)
	status.Services[idx].State = ServiceStateMigrated
	status.Services[idx].MigrationVersion = version
	status.UpdatedAt = timeNow()
	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Warn("failed to save migration status", "error", err, "service", svc.Name)
	}

	return false, nil
}

// handleCircuitBreakerError checks if an error is a circuit breaker error and updates status accordingly.
// Returns true if the service was skipped (circuit breaker open/half-open), false otherwise.
func (p *PostgresProvisioner) handleCircuitBreakerError(err error, status *ProvisioningStatus, idx int, svc ServiceConfig, logger *slog.Logger) bool {
	if errors.Is(err, gobreaker.ErrOpenState) {
		retryAfter := timeNow().Add(BreakerTimeout)
		logger.Warn("circuit breaker open, skipping service",
			"service", svc.Name,
			"breaker_state", "open",
			"retry_after", retryAfter.Format(time.RFC3339))
		observability.IncrementServiceFailure(svc.Name)
		status.Services[idx].State = ServiceStateCircuitOpen
		status.Services[idx].ErrorMessage = fmt.Sprintf(
			"circuit breaker open for %s: too many recent failures. Retry after %s",
			svc.Name, retryAfter.Format(time.RFC3339))
		return true
	}
	if errors.Is(err, gobreaker.ErrTooManyRequests) {
		logger.Info("circuit breaker half-open, too many test requests",
			"service", svc.Name,
			"breaker_state", "half-open",
			"max_requests", BreakerMaxRequests)
		observability.IncrementServiceFailure(svc.Name)
		status.Services[idx].State = ServiceStateCircuitOpen
		status.Services[idx].ErrorMessage = fmt.Sprintf(
			"circuit breaker half-open for %s: max test requests (%d) exceeded. Waiting for test results",
			svc.Name, BreakerMaxRequests)
		return true
	}
	return false
}

// applyMigrationsWithCircuitBreaker wraps the migration execution with circuit breaker protection.
// Returns the migration version on success, or an error (which may be circuit breaker errors).
func (p *PostgresProvisioner) applyMigrationsWithCircuitBreaker(
	ctx context.Context,
	breaker *gobreaker.CircuitBreaker[any],
	db *gorm.DB,
	schemaName string,
	svc ServiceConfig,
	logger *slog.Logger,
) (string, error) {
	// Track if we're in half-open state before execution (to log transition to closed)
	stateBefore := breaker.State()

	result, err := breaker.Execute(func() (any, error) {
		version, migErr := p.applyServiceMigrationsToDB(ctx, db, schemaName, svc)
		if migErr != nil {
			return nil, migErr
		}
		return version, nil
	})
	if err != nil {
		return "", err
	}

	// Log successful execution that transitioned circuit from half-open to closed
	stateAfter := breaker.State()
	if stateBefore == gobreaker.StateHalfOpen && stateAfter == gobreaker.StateClosed {
		logger.Info("circuit breaker closed after successful execution",
			"service", svc.Name,
			"previous_state", "half-open",
			"new_state", "closed")
	}

	// Type assert the result
	version, ok := result.(string)
	if !ok {
		return "", nil // Empty version is valid
	}
	return version, nil
}

// markProvisioningFailed updates status to failed state with error message.
// Uses a fresh context for the save operation since the original context may be
// cancelled (e.g., due to timeout), and we still want to record the failure.
func (p *PostgresProvisioner) markProvisioningFailed(_ context.Context, status *ProvisioningStatus, errorMsg string) {
	status.State = StateFailed
	status.ErrorMessage = errorMsg
	status.UpdatedAt = timeNow()

	// Use a fresh context with timeout for cleanup, since the original may be cancelled.
	// This is intentional - we want to save the failed status even if the parent context
	// was cancelled due to timeout, so we use context.Background() as the base.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.saveProvisioningStatus(cleanupCtx, status); err != nil { //nolint:contextcheck // cleanupCtx intentionally derived from Background() for cleanup after parent timeout
		p.logger.Warn("failed to save failed status",
			"tenant_id", status.TenantID.String(),
			"error", err)
	}
}

// runPostProvisioningHooks executes all registered post-provisioning hooks.
// Hook failures are logged but do NOT fail provisioning since schemas are ready.
func (p *PostgresProvisioner) runPostProvisioningHooks(ctx context.Context, tenantID tenant.TenantID, logger *slog.Logger) {
	if len(p.config.PostProvisioningHooks) == 0 {
		return
	}

	logger.Debug("running post-provisioning hooks", "hook_count", len(p.config.PostProvisioningHooks))

	for i, hook := range p.config.PostProvisioningHooks {
		if err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("%w: %v", ErrHookPanic, r)
				}
			}()
			return hook(ctx, tenantID)
		}(); err != nil {
			logger.Warn("post-provisioning hook failed",
				"hook_index", i,
				"error", err,
				"note", "provisioning succeeded despite hook failure")
		} else {
			logger.Debug("post-provisioning hook completed", "hook_index", i)
		}
	}
}

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

// Ensure PostgresProvisioner implements SchemaProvisioner.
var _ SchemaProvisioner = (*PostgresProvisioner)(nil)
