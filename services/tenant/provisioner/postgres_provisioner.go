// Package provisioner provides PostgreSQL schema provisioning for multi-tenant isolation.
package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/sony/gobreaker/v2"
	"gorm.io/driver/postgres"
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

// openServiceConnections opens database connections for all services.
// On failure, it closes any already-opened connections.
func openServiceConnections(services []ServiceConfig) (map[string]*gorm.DB, error) {
	serviceDbs := make(map[string]*gorm.DB)

	for _, svc := range services {
		db, err := gorm.Open(postgres.Open(svc.DatabaseURL), &gorm.Config{
			SkipDefaultTransaction: true,
			PrepareStmt:            true,
		})
		if err != nil {
			closeServiceConnections(serviceDbs)
			return nil, fmt.Errorf("failed to connect to %s database: %w", svc.Name, err)
		}

		sqlDB, err := db.DB()
		if err != nil {
			// Note: If db.DB() fails, we cannot properly close this GORM connection
			// since that requires the underlying *sql.DB. Log the potential leak.
			slog.Default().Error("failed to get underlying DB (connection may leak)",
				"service", svc.Name, "error", err)
			closeServiceConnections(serviceDbs)
			return nil, fmt.Errorf("get underlying DB for %s: %w", svc.Name, err)
		}

		// Configure connection pool - provisioner doesn't need many connections
		sqlDB.SetMaxOpenConns(5)
		sqlDB.SetMaxIdleConns(2)
		sqlDB.SetConnMaxLifetime(time.Hour)

		serviceDbs[svc.Name] = db
	}

	return serviceDbs, nil
}

// closeServiceConnections closes all connections in the map.
// Used for cleanup during initialization failures.
func closeServiceConnections(serviceDbs map[string]*gorm.DB) {
	for name, db := range serviceDbs {
		if sqlDB, err := db.DB(); err == nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				slog.Default().Warn("failed to close database during cleanup",
					"service", name, "error", closeErr)
			}
		}
	}
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
// Idempotency is guaranteed through multiple layers:
//
//  1. State check with early return: If tenant is already 'active', returns immediately
//     without modifying any state (see prepareProvisioningStatus).
//
//  2. CREATE SCHEMA IF NOT EXISTS: Schema creation uses PostgreSQL's IF NOT EXISTS clause,
//     making it safe to retry after partial failures.
//
//  3. Migration error handling: If tables/indexes already exist (PostgreSQL error codes
//     42P07, 42P06, 42710), the migration continues rather than failing. This handles
//     cases where a previous attempt partially completed.
//
//  4. Mutex protection: A sync.Mutex prevents concurrent provisioning of the same tenant
//     within a single process instance. Cross-process protection is provided by the
//     'in_progress' state check in the database.
//
//  5. No destructive operations: This method never executes DROP, TRUNCATE, or DELETE
//     statements. Failed provisioning can always be safely retried.
//
// Thread-safety: Safe for concurrent calls. The mutex serializes provisioning for the
// same tenant, while different tenants can be provisioned in parallel.
func (p *PostgresProvisioner) ProvisionSchemas(ctx context.Context, tenantID tenant.TenantID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	startTime := time.Now()
	logger := p.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("starting schema provisioning", "services_count", len(p.config.Services))

	// Validate and prepare provisioning status.
	// IDEMPOTENCY: If tenant is already 'active', prepareProvisioningStatus returns
	// errAlreadyProvisioned, and we return nil (success) without any modifications.
	status, err := p.prepareProvisioningStatus(ctx, tenantID)
	if errors.Is(err, errAlreadyProvisioned) {
		logger.Info("schema already provisioned, skipping")
		return nil // Idempotent: already provisioned, no-op success
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

	// Check context before expensive operations
	if ctx.Err() != nil {
		logger.Warn("context cancelled before schema creation")
		p.markProvisioningFailed(ctx, status, "cancelled before schema creation")
		return ErrProvisioningTimeout
	}

	// Create the schema IN EACH SERVICE DATABASE.
	// IDEMPOTENCY: Uses CREATE SCHEMA IF NOT EXISTS (see createSchemaInDB), so this is
	// safe to call multiple times - existing schemas are silently skipped.
	// Iterate over config.Services for deterministic order (easier debugging).
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

	// Mark as active
	status.State = StateActive
	status.UpdatedAt = time.Now()
	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		logger.Error("failed to save final status", "error", err)
		return fmt.Errorf("save final status: %w", err)
	}

	logger.Info("schema provisioning completed",
		"duration_ms", time.Since(startTime).Milliseconds(),
		"services_count", len(p.config.Services))
	return nil
}

// prepareProvisioningStatus validates existing status and prepares for provisioning.
//
// IDEMPOTENCY: This is the primary idempotency gate for ProvisionSchemas:
//   - StateActive: Returns errAlreadyProvisioned (caller treats as success/no-op)
//   - StateInProgress: Returns ErrProvisioningInProgress (prevents concurrent provisioning)
//   - StateDeprovisioned: Returns ErrAlreadyDeprovisioned (terminal state, cannot re-provision)
//   - StatePending/StateFailed: Proceeds with provisioning (retry is allowed)
//
// Returns nil status if already active (no-op), or error if provisioning cannot proceed.
func (p *PostgresProvisioner) prepareProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil && !errors.Is(err, ErrProvisioningStatusNotFound) {
		return nil, fmt.Errorf("get provisioning status: %w", err)
	}

	// Check existing state
	if status != nil {
		switch status.State {
		case StateActive:
			return nil, errAlreadyProvisioned // Idempotent: already provisioned
		case StateInProgress:
			return nil, ErrProvisioningInProgress
		case StateDeprovisioned:
			return nil, ErrAlreadyDeprovisioned
		case StatePending, StateFailed:
			// Can proceed with provisioning/retry
		}
	}

	// Create or update status to in_progress
	now := time.Now()
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

		// Get the database connection for this service
		serviceDB, ok := p.serviceDbs[svc.Name]
		if !ok {
			logger.Error("service database not found", "service", svc.Name)
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("no database connection for service: %s", svc.Name))
			return fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name)
		}

		logger.Debug("provisioning service",
			"service", svc.Name,
			"migration_path", svc.MigrationPath,
			"database_url", maskDatabaseURL(svc.DatabaseURL))

		// Update service status to created
		status.Services[i].State = ServiceStateCreated
		status.UpdatedAt = time.Now()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			logger.Warn("failed to save intermediate status", "error", err, "service", svc.Name)
		}

		// Apply migrations through circuit breaker
		breaker := p.circuitBreakers.GetBreaker(svc.Name)
		version, err := p.applyMigrationsWithCircuitBreaker(ctx, breaker, serviceDB, schemaName, svc, logger)

		// Handle circuit breaker specific errors
		if errors.Is(err, gobreaker.ErrOpenState) {
			retryAfter := time.Now().Add(BreakerTimeout)
			logger.Warn("circuit breaker open, skipping service",
				"service", svc.Name,
				"breaker_state", "open",
				"retry_after", retryAfter.Format(time.RFC3339))
			status.Services[i].State = ServiceStateCircuitOpen
			status.Services[i].ErrorMessage = fmt.Sprintf(
				"circuit breaker open for %s: too many recent failures. Retry after %s",
				svc.Name, retryAfter.Format(time.RFC3339))
			skippedServices = append(skippedServices, svc.Name)
			continue // Skip this service but continue with others
		}
		if errors.Is(err, gobreaker.ErrTooManyRequests) {
			logger.Info("circuit breaker half-open, too many test requests",
				"service", svc.Name,
				"breaker_state", "half-open",
				"max_requests", BreakerMaxRequests)
			status.Services[i].State = ServiceStateCircuitOpen
			status.Services[i].ErrorMessage = fmt.Sprintf(
				"circuit breaker half-open for %s: max test requests (%d) exceeded. Waiting for test results",
				svc.Name, BreakerMaxRequests)
			skippedServices = append(skippedServices, svc.Name)
			continue // Skip this service but continue with others
		}

		if err != nil {
			logger.Error("service migration failed", "service", svc.Name, "error", err)
			status.Services[i].State = ServiceStateFailed
			status.Services[i].ErrorMessage = err.Error()
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("%s migrations failed: %v", svc.Name, err))
			return fmt.Errorf("%w: %s: %v", ErrMigrationFailed, svc.Name, err) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
		}

		logger.Debug("service migration completed", "service", svc.Name, "version", version)
		status.Services[i].State = ServiceStateMigrated
		status.Services[i].MigrationVersion = version
		status.UpdatedAt = time.Now()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			logger.Warn("failed to save migration status", "error", err, "service", svc.Name)
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

// applyMigrationsWithCircuitBreaker wraps the migration execution with circuit breaker protection.
// Returns the migration version on success, or an error (which may be circuit breaker errors).
//
// Circuit breaker state logging is handled by the caller (provisionAllServices) at appropriate
// log levels: WARN for open state, INFO for half-open. On success after half-open, this function
// logs at INFO level to indicate the circuit has closed.
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
		// Circuit breaker errors are logged by the caller at appropriate levels
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

// maskDatabaseURL redacts password from connection string for logging.
// Uses url.Parse for robust handling of various URL formats.
func maskDatabaseURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}

// markProvisioningFailed updates status to failed state with error message.
// Uses a fresh context for the save operation since the original context may be
// cancelled (e.g., due to timeout), and we still want to record the failure.
func (p *PostgresProvisioner) markProvisioningFailed(_ context.Context, status *ProvisioningStatus, errorMsg string) {
	status.State = StateFailed
	status.ErrorMessage = errorMsg
	status.UpdatedAt = time.Now()

	// Use a fresh context with timeout for cleanup, since the original may be cancelled.
	// This is intentional - we want to save the failed status even if the parent context
	// was cancelled due to timeout, so we use context.Background() as the base.
	//nolint:contextcheck // Intentionally using Background() for cleanup after timeout
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	//nolint:contextcheck // cleanupCtx intentionally derived from Background() for cleanup after timeout
	if err := p.saveProvisioningStatus(cleanupCtx, status); err != nil {
		p.logger.Warn("failed to save failed status",
			"tenant_id", status.TenantID.String(),
			"error", err)
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
	now := time.Now()
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
		if time.Now().Before(retentionEnd) {
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

// ReconcileMigrations detects and applies new migrations to existing tenant schemas.
//
// This method addresses schema drift that occurs when services add new migrations
// after tenants are created. It scans migration directories, compares with
// the recorded MigrationVersion for each service, and applies any newer migrations.
//
// If tenantID is nil, all active tenants are reconciled. Individual tenant failures
// don't stop processing of other tenants - errors are collected and returned.
func (p *PostgresProvisioner) ReconcileMigrations(ctx context.Context, tenantID *tenant.TenantID) (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	logger := p.logger.With("operation", "reconcile_migrations")

	// Determine which tenants to reconcile
	tenantsToReconcile, err := p.getTenantsToReconcile(ctx, tenantID)
	if err != nil {
		logger.Error("failed to get tenants to reconcile", "error", err)
		return 0, []string{fmt.Sprintf("get tenants: %v", err)}
	}

	if len(tenantsToReconcile) == 0 {
		logger.Info("no tenants to reconcile")
		return 0, nil
	}

	logger.Info("starting migration reconciliation", "tenant_count", len(tenantsToReconcile))

	var (
		reconciledCount int
		errs            []string
	)

	for _, tid := range tenantsToReconcile {
		if ctx.Err() != nil {
			errs = append(errs, fmt.Sprintf("context cancelled: %v", ctx.Err()))
			break
		}

		applied, err := p.reconcileTenantMigrations(ctx, tid)
		if err != nil {
			logger.Error("failed to reconcile tenant",
				"tenant_id", tid.String(),
				"error", err)
			errs = append(errs, fmt.Sprintf("%s: %v", tid.String(), err))
			continue
		}

		if applied {
			reconciledCount++
			logger.Info("tenant migrations reconciled", "tenant_id", tid.String())
		}
	}

	logger.Info("migration reconciliation completed",
		"reconciled_count", reconciledCount,
		"error_count", len(errs))

	return reconciledCount, errs
}

// getTenantsToReconcile returns the list of tenant IDs to reconcile.
// If tenantID is non-nil, returns just that tenant. Otherwise returns all active tenants.
func (p *PostgresProvisioner) getTenantsToReconcile(ctx context.Context, tenantID *tenant.TenantID) ([]tenant.TenantID, error) {
	if tenantID != nil {
		return []tenant.TenantID{*tenantID}, nil
	}

	// Query all active tenants from the platform database
	var entities []provisioningEntity
	result := p.platformDB.WithContext(ctx).
		Where("state = ?", string(StateActive)).
		Find(&entities)
	if result.Error != nil {
		return nil, fmt.Errorf("query active tenants: %w", result.Error)
	}

	tenants := make([]tenant.TenantID, 0, len(entities))
	for _, entity := range entities {
		tid, err := tenant.NewTenantID(entity.TenantID)
		if err != nil {
			p.logger.Warn("invalid tenant ID in provisioning table",
				"tenant_id", entity.TenantID,
				"error", err)
			continue
		}
		tenants = append(tenants, tid)
	}

	return tenants, nil
}

// reconcileTenantMigrations applies new migrations to a single tenant.
// Returns true if any migrations were applied, false otherwise.
func (p *PostgresProvisioner) reconcileTenantMigrations(ctx context.Context, tenantID tenant.TenantID) (bool, error) {
	logger := p.logger.With("tenant_id", tenantID.String())

	// Get current provisioning status
	status, err := p.getProvisioningStatusLocked(ctx, tenantID)
	if err != nil {
		return false, fmt.Errorf("get provisioning status: %w", err)
	}

	// Only reconcile active tenants
	if status.State != StateActive {
		logger.Debug("skipping non-active tenant", "state", status.State)
		return false, nil
	}

	schemaName := tenantID.SchemaName()
	anyMigrationsApplied := false

	// Check each service for new migrations
	for _, svc := range p.config.Services {
		// Get the current version for this service by name (not index)
		// This handles cases where services are added/removed from config after provisioning
		svcStatus := status.getServiceStatus(svc.Name)
		currentVersion := ""
		if svcStatus != nil {
			currentVersion = svcStatus.MigrationVersion
		}

		// Warn if service has no recorded version - could indicate config drift
		if currentVersion == "" {
			logger.Warn("service has no recorded migration version, skipping reconciliation",
				"service", svc.Name,
				"hint", "this may indicate the service was added after initial provisioning")
			continue
		}

		// Read all migration files
		migrations, err := p.readMigrationFiles(svc.MigrationPath)
		if err != nil {
			return anyMigrationsApplied, fmt.Errorf("read migrations for %s: %w", svc.Name, err)
		}

		// Filter migrations newer than current version
		newMigrations := filterMigrationsAfter(migrations, currentVersion)

		if len(newMigrations) == 0 {
			logger.Debug("no new migrations for service", "service", svc.Name)
			continue
		}

		logger.Info("applying new migrations",
			"service", svc.Name,
			"current_version", currentVersion,
			"new_migration_count", len(newMigrations))

		// Get the database connection for this service
		serviceDB, ok := p.serviceDbs[svc.Name]
		if !ok {
			return anyMigrationsApplied, fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name)
		}

		// Apply new migrations
		latestVersion, err := p.applyMigrationList(ctx, serviceDB, schemaName, newMigrations)
		if err != nil {
			return anyMigrationsApplied, fmt.Errorf("apply migrations for %s: %w", svc.Name, err)
		}

		// Update service status by name
		if svcStatus != nil {
			svcStatus.MigrationVersion = latestVersion
		}
		anyMigrationsApplied = true

		logger.Debug("service migrations applied",
			"service", svc.Name,
			"new_version", latestVersion)
	}

	// Save updated status if any migrations were applied
	if anyMigrationsApplied {
		status.UpdatedAt = time.Now()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			return true, fmt.Errorf("save updated status: %w", err)
		}
	}

	return anyMigrationsApplied, nil
}

// filterMigrationsAfter returns migrations with version > currentVersion.
// Migrations are already sorted by filename/version from readMigrationFiles.
//
// If currentVersion is empty, returns nil as a safety guard. The caller
// (reconcileTenantMigrations) handles empty versions explicitly with a warning.
func filterMigrationsAfter(migrations []migration, currentVersion string) []migration {
	if currentVersion == "" {
		return nil
	}

	var result []migration
	for _, mig := range migrations {
		if mig.Version > currentVersion {
			result = append(result, mig)
		}
	}
	return result
}

// applyMigrationList applies a specific list of migrations to a tenant schema.
// This is extracted from applyServiceMigrationsToDB to support both initial
// provisioning (all migrations) and reconciliation (subset of migrations).
//
// IDEMPOTENCY: Same guarantees as applyServiceMigrationsToDB - objects that already
// exist are silently skipped via isAlreadyExistsError.
func (p *PostgresProvisioner) applyMigrationList(ctx context.Context, db *gorm.DB, schemaName string, migrations []migration) (string, error) {
	if len(migrations) == 0 {
		return "", nil
	}

	// Set search_path to the tenant schema for unqualified table names
	setPathQuery := fmt.Sprintf("SET search_path TO %s, public", quoteIdentifier(schemaName))

	var lastVersion string
	for _, mig := range migrations {
		if ctx.Err() != nil {
			return lastVersion, ctx.Err()
		}

		// Execute migration within a transaction
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec(setPathQuery).Error; err != nil {
				return fmt.Errorf("set search_path: %w", err)
			}

			processedSQL := p.processMigrationSQL(mig.Content, schemaName)
			statements := splitSQLStatements(processedSQL)

			for _, stmt := range statements {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("execute migration %s: %w", mig.Filename, err)
				}
			}

			return nil
		})
		if err != nil {
			// IDEMPOTENCY: If error indicates objects already exist (duplicate_table,
			// duplicate_schema, duplicate_object), treat as success. This handles the
			// case where a previous provisioning attempt partially completed.
			if isAlreadyExistsError(err) {
				lastVersion = mig.Version
				continue
			}
			return lastVersion, err
		}

		lastVersion = mig.Version
	}

	return lastVersion, nil
}

// createSchemaInDB creates the org_{tenant_id} schema in the specified database.
//
// IDEMPOTENCY: Uses CREATE SCHEMA IF NOT EXISTS, so calling this multiple times
// for the same schema is safe - PostgreSQL silently ignores the request if the
// schema already exists.
func (p *PostgresProvisioner) createSchemaInDB(ctx context.Context, db *gorm.DB, schemaName string) error {
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schemaName))
	return db.WithContext(ctx).Exec(query).Error
}

// dropSchemaInAllDBs drops the org_{tenant_id} schema from all service databases.
// This function attempts to drop schemas from ALL databases, collecting errors
// along the way rather than failing on the first error. This ensures best-effort
// cleanup even when some databases encounter issues.
func (p *PostgresProvisioner) dropSchemaInAllDBs(ctx context.Context, schemaName string) error {
	var errs []error
	// Iterate over config.Services for deterministic order (easier debugging)
	for _, svc := range p.config.Services {
		serviceDB, ok := p.serviceDbs[svc.Name]
		if !ok || serviceDB == nil {
			errs = append(errs, fmt.Errorf("%w: %s", ErrServiceDatabaseNotFound, svc.Name))
			continue
		}
		query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quoteIdentifier(schemaName))
		if err := serviceDB.WithContext(ctx).Exec(query).Error; err != nil {
			errs = append(errs, fmt.Errorf("drop schema in %s: %w", svc.Name, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrDeprovisioningFailed, errors.Join(errs...)) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}
	return nil
}

// applyServiceMigrationsToDB applies all migration files for a service to the tenant schema
// in the specified service database.
//
// IDEMPOTENCY: If a migration creates objects that already exist, the error is caught by
// isAlreadyExistsError and the migration is marked as applied. This allows retries after
// partial failures where some tables were created but the version wasn't recorded.
//
// Returns the version string of the last applied migration.
func (p *PostgresProvisioner) applyServiceMigrationsToDB(ctx context.Context, db *gorm.DB, schemaName string, svc ServiceConfig) (string, error) {
	// Read migration files
	migrations, err := p.readMigrationFiles(svc.MigrationPath)
	if err != nil {
		return "", fmt.Errorf("read migrations: %w", err)
	}

	if len(migrations) == 0 {
		return "", nil
	}

	// Set search_path to the tenant schema for unqualified table names
	setPathQuery := fmt.Sprintf("SET search_path TO %s, public", quoteIdentifier(schemaName))

	var lastVersion string
	for _, mig := range migrations {
		// Check context cancellation
		if ctx.Err() != nil {
			return lastVersion, ctx.Err()
		}

		// Execute migration within a transaction ON SERVICE DATABASE
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Set search_path
			if err := tx.Exec(setPathQuery).Error; err != nil {
				return fmt.Errorf("set search_path: %w", err)
			}

			// Process the migration content to remove schema prefixes
			processedSQL := p.processMigrationSQL(mig.Content, schemaName)

			// Split into individual statements and execute one at a time
			// CockroachDB doesn't support multiple statements in prepared statements
			statements := splitSQLStatements(processedSQL)
			for _, stmt := range statements {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("execute migration %s: %w", mig.Filename, err)
				}
			}

			return nil
		})
		if err != nil {
			// IDEMPOTENCY: If error indicates objects already exist (duplicate_table,
			// duplicate_schema, duplicate_object), treat as success. This handles the
			// case where a previous provisioning attempt partially completed - some
			// tables were created but the migration version wasn't recorded.
			if isAlreadyExistsError(err) {
				lastVersion = mig.Version
				continue
			}
			return lastVersion, err
		}

		lastVersion = mig.Version
	}

	return lastVersion, nil
}

// migration represents a single migration file.
type migration struct {
	Filename string
	Version  string
	Content  string
}

// readMigrationFiles reads all .sql files from the migration path, sorted by filename.
func (p *PostgresProvisioner) readMigrationFiles(migrationPath string) ([]migration, error) {
	entries, err := os.ReadDir(migrationPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No migrations directory is valid
		}
		return nil, fmt.Errorf("read directory: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(migrationPath, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", entry.Name(), err)
		}

		// Extract version from filename (e.g., "20251208211142_initial.sql" -> "20251208211142")
		version := strings.TrimSuffix(entry.Name(), ".sql")
		if idx := strings.Index(version, "_"); idx > 0 {
			version = version[:idx]
		}

		migrations = append(migrations, migration{
			Filename: entry.Name(),
			Version:  version,
			Content:  string(content),
		})
	}

	// Sort by filename to ensure consistent order
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Filename < migrations[j].Filename
	})

	return migrations, nil
}

// processMigrationSQL processes migration SQL to work with dynamic schema names.
// It handles both unqualified table names and hardcoded schema references.
//
// Security: The schemaName parameter is derived from TenantID.SchemaName(),
// which is validated at construction to contain only alphanumeric characters and
// underscores (regex: ^[a-zA-Z0-9_]{1,50}$). The "org_" prefix is added and the
// string is lowercased, making SQL injection impossible through this path.
// The string replacement is safe because the schema name cannot contain quotes,
// semicolons, or other SQL control characters.
func (p *PostgresProvisioner) processMigrationSQL(sql, schemaName string) string {
	// Remove CREATE SCHEMA statements - we already created the schema
	lines := strings.Split(sql, "\n")
	filteredLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.ToUpper(line))
		if strings.HasPrefix(trimmed, "CREATE SCHEMA") {
			continue
		}
		filteredLines = append(filteredLines, line)
	}
	sql = strings.Join(filteredLines, "\n")

	// Replace hardcoded schema references with the tenant schema
	// Common patterns in the codebase: "current_account"."table", "party"."table", etc.
	schemaPatterns := []string{
		"current_account",
		"party",
		"position_keeping",
		"financial_accounting",
		"payment_order",
	}

	for _, pattern := range schemaPatterns {
		// Replace "schema"."table" with "tenant_schema"."table"
		sql = strings.ReplaceAll(sql, `"`+pattern+`".`, `"`+schemaName+`".`)
		// Also handle unquoted references
		sql = strings.ReplaceAll(sql, pattern+".", schemaName+".")
	}

	return sql
}

// splitSQLStatements splits SQL content into individual statements.
// Handles semicolons inside single quotes and comments.
// CockroachDB requires statements to be executed individually.
//
//nolint:gocognit,gocyclo // State machine for SQL parsing necessarily has multiple conditions
func splitSQLStatements(sql string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(sql); i++ {
		c := sql[i]

		// Handle line comments
		if !inString && !inBlockComment && i+1 < len(sql) && c == '-' && sql[i+1] == '-' {
			inLineComment = true
		}
		if inLineComment && c == '\n' {
			inLineComment = false
		}

		// Handle block comments
		if !inString && !inLineComment && i+1 < len(sql) && c == '/' && sql[i+1] == '*' {
			inBlockComment = true
		}
		if inBlockComment && i+1 < len(sql) && c == '*' && sql[i+1] == '/' {
			current.WriteByte(c)
			current.WriteByte(sql[i+1])
			i++
			inBlockComment = false
			continue
		}

		// Handle string literals (single quotes)
		if !inLineComment && !inBlockComment && c == '\'' {
			// Check for escaped quote ''
			if i+1 < len(sql) && sql[i+1] == '\'' {
				current.WriteByte(c)
				current.WriteByte(sql[i+1])
				i++
				continue
			}
			inString = !inString
		}

		// Split on semicolon outside strings and comments
		if c == ';' && !inString && !inLineComment && !inBlockComment {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}

		current.WriteByte(c)
	}

	// Add final statement if any
	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		statements = append(statements, stmt)
	}

	return statements
}

// createInitialServiceStatuses creates the initial service status list.
func (p *PostgresProvisioner) createInitialServiceStatuses(tenantID tenant.TenantID) []ServiceSchemaStatus {
	statuses := make([]ServiceSchemaStatus, len(p.config.Services))
	schemaName := tenantID.SchemaName()

	for i, svc := range p.config.Services {
		statuses[i] = ServiceSchemaStatus{
			ServiceName: svc.Name,
			SchemaName:  schemaName,
			State:       ServiceStatePending,
		}
	}

	return statuses
}

// Database entity for tenant_provisioning table.
type provisioningEntity struct {
	TenantID        string     `gorm:"column:tenant_id;primaryKey"`
	State           string     `gorm:"column:state"`
	ServiceSchemas  string     `gorm:"column:service_schemas;type:jsonb"`
	ErrorMessage    *string    `gorm:"column:error_message"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	DeprovisionedAt *time.Time `gorm:"column:deprovisioned_at"`
	Version         int        `gorm:"column:version"`
}

// TableName specifies the table name for GORM.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries.
func (provisioningEntity) TableName() string {
	return "tenant_provisioning"
}

// getProvisioningStatusLocked retrieves the status without acquiring the mutex.
// Caller must hold the mutex.
func (p *PostgresProvisioner) getProvisioningStatusLocked(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error) {
	var entity provisioningEntity
	result := p.platformDB.WithContext(ctx).Where("tenant_id = ?", tenantID.String()).First(&entity)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, ErrProvisioningStatusNotFound
	}
	if result.Error != nil {
		return nil, result.Error
	}

	return p.entityToStatus(tenantID, &entity)
}

// saveProvisioningStatus saves or updates the provisioning status using upsert.
func (p *PostgresProvisioner) saveProvisioningStatus(ctx context.Context, status *ProvisioningStatus) error {
	entity, err := p.statusToEntity(status)
	if err != nil {
		return err
	}

	// Use PostgreSQL UPSERT (ON CONFLICT DO UPDATE)
	// This is atomic and handles both insert and update cases
	// Uses unqualified table name (tenant_provisioning) for search_path routing
	upsertSQL := `
		INSERT INTO tenant_provisioning
			(tenant_id, state, service_schemas, error_message, created_at, updated_at, deprovisioned_at, version)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (tenant_id) DO UPDATE SET
			state = EXCLUDED.state,
			service_schemas = EXCLUDED.service_schemas,
			error_message = EXCLUDED.error_message,
			updated_at = EXCLUDED.updated_at,
			deprovisioned_at = EXCLUDED.deprovisioned_at,
			version = tenant_provisioning.version + 1
	`

	return p.platformDB.WithContext(ctx).Exec(
		upsertSQL,
		entity.TenantID,
		entity.State,
		entity.ServiceSchemas,
		entity.ErrorMessage,
		entity.CreatedAt,
		entity.UpdatedAt,
		entity.DeprovisionedAt,
	).Error
}

// deleteProvisioningStatus removes the provisioning status record.
func (p *PostgresProvisioner) deleteProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) error {
	return p.platformDB.WithContext(ctx).
		Where("tenant_id = ?", tenantID.String()).
		Delete(&provisioningEntity{}).Error
}

// entityToStatus converts database entity to domain model.
func (p *PostgresProvisioner) entityToStatus(tenantID tenant.TenantID, entity *provisioningEntity) (*ProvisioningStatus, error) {
	var services []ServiceSchemaStatus
	if entity.ServiceSchemas != "" && entity.ServiceSchemas != "[]" {
		if err := json.Unmarshal([]byte(entity.ServiceSchemas), &services); err != nil {
			return nil, fmt.Errorf("unmarshal service_schemas: %w", err)
		}
	}

	status := &ProvisioningStatus{
		TenantID:        tenantID,
		State:           ProvisioningState(entity.State),
		Services:        services,
		CreatedAt:       entity.CreatedAt,
		UpdatedAt:       entity.UpdatedAt,
		DeprovisionedAt: entity.DeprovisionedAt,
	}

	if entity.ErrorMessage != nil {
		status.ErrorMessage = *entity.ErrorMessage
	}

	return status, nil
}

// statusToEntity converts domain model to database entity.
func (p *PostgresProvisioner) statusToEntity(status *ProvisioningStatus) (*provisioningEntity, error) {
	servicesJSON, err := json.Marshal(status.Services)
	if err != nil {
		return nil, fmt.Errorf("marshal services: %w", err)
	}

	entity := &provisioningEntity{
		TenantID:        status.TenantID.String(),
		State:           string(status.State),
		ServiceSchemas:  string(servicesJSON),
		CreatedAt:       status.CreatedAt,
		UpdatedAt:       status.UpdatedAt,
		DeprovisionedAt: status.DeprovisionedAt,
		Version:         1, // Will be incremented during update
	}

	if status.ErrorMessage != "" {
		entity.ErrorMessage = &status.ErrorMessage
	}

	return entity, nil
}

// quoteIdentifier safely quotes a PostgreSQL identifier to prevent SQL injection.
// Uses double quotes and escapes any embedded double quotes.
func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// isAlreadyExistsError checks if the error indicates an object already exists.
//
// IDEMPOTENCY: This is a key idempotency mechanism for migrations. When a migration
// attempts to create a table, index, or schema that already exists, we treat it as
// success rather than failure. This handles partial provisioning retries gracefully.
//
// Recognized PostgreSQL error codes:
//   - 42P07: duplicate_table (table already exists)
//   - 42P06: duplicate_schema (schema already exists)
//   - 42710: duplicate_object (index or other object already exists)
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	// Check for PostgreSQL-specific error codes
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42P07: duplicate_table
		// 42P06: duplicate_schema
		// 42710: duplicate_object (for indexes)
		switch pgErr.Code {
		case "42P07", "42P06", "42710":
			return true
		}
	}

	// Fallback to string matching
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "already exists") ||
		strings.Contains(errStr, "duplicate")
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
	now := time.Now()
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
// The caller (typically main.go) is responsible for closing platformDB separately,
// as it may be shared with other components.
func (p *PostgresProvisioner) Close() error {
	var errs []error
	// Iterate over config.Services for deterministic order (easier debugging)
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
// This method is useful for monitoring dashboards and health checks to observe:
//   - Which services have open circuit breakers
//   - Request counts and failure statistics
//   - When services are in half-open state (testing recovery)
//
// Returns a slice of CircuitBreakerState, one for each service that has been accessed.
// Services that have never been provisioned will not have a circuit breaker entry.
func (p *PostgresProvisioner) GetCircuitBreakerStates() []CircuitBreakerState {
	return p.circuitBreakers.GetAllCircuitBreakerStates()
}

// GetCircuitBreakerState returns the current state of the circuit breaker for a specific service.
// Returns nil if the service has never been accessed (no circuit breaker exists).
func (p *PostgresProvisioner) GetCircuitBreakerState(serviceName string) *CircuitBreakerState {
	return p.circuitBreakers.GetCircuitBreakerState(serviceName)
}

// Ensure PostgresProvisioner implements SchemaProvisioner.
var _ SchemaProvisioner = (*PostgresProvisioner)(nil)
