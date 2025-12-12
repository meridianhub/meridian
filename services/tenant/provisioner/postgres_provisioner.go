// Package provisioner provides PostgreSQL schema provisioning for multi-tenant isolation.
package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"gorm.io/gorm"
)

// errAlreadyProvisioned is an internal sentinel for idempotent provisioning.
// It's not a public error because it's not an actual error condition.
var errAlreadyProvisioned = errors.New("already provisioned")

// PostgresProvisioner implements SchemaProvisioner for PostgreSQL databases.
// It creates org_{tenant_id} schemas and applies service migrations using SQL files.
//
// Thread-safe for concurrent use via mutex protection around status operations.
type PostgresProvisioner struct {
	db     *gorm.DB
	config *Config
	logger *slog.Logger

	// mu protects concurrent access to provisioning operations for the same tenant.
	// This prevents race conditions when multiple goroutines attempt to provision
	// the same tenant simultaneously.
	mu sync.Mutex
}

// NewPostgresProvisioner creates a new PostgreSQL schema provisioner.
// Returns an error if the config is invalid.
func NewPostgresProvisioner(db *gorm.DB, config *Config) (*PostgresProvisioner, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &PostgresProvisioner{
		db:     db,
		config: config,
		logger: slog.Default().With("component", "provisioner"),
	}, nil
}

// ProvisionSchemas creates the org_{tenant_id} schema and applies all service migrations.
//
// The function performs the following steps:
//  1. Creates or updates the provisioning status record to 'in_progress'
//  2. Creates the org_{tenant_id} schema (CREATE SCHEMA IF NOT EXISTS)
//  3. For each configured service, applies migrations to the tenant schema
//  4. Updates the provisioning status to 'active' on success or 'failed' on error
//
// Idempotency: Safe to call multiple times. Already-created schemas are skipped,
// and migrations are checked against the version recorded in the status.
func (p *PostgresProvisioner) ProvisionSchemas(ctx context.Context, tenantID organization.OrganizationID) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	startTime := time.Now()
	logger := p.logger.With("tenant_id", tenantID.String(), "schema", tenantID.SchemaName())
	logger.Info("starting schema provisioning")

	// Validate and prepare provisioning status
	status, err := p.prepareProvisioningStatus(ctx, tenantID)
	if errors.Is(err, errAlreadyProvisioned) {
		logger.Info("schema already provisioned, skipping")
		return nil // Idempotent: already provisioned
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

	// Create the schema
	schemaName := tenantID.SchemaName()
	if err := p.createSchema(ctx, schemaName); err != nil {
		logger.Error("failed to create schema", "error", err)
		p.markProvisioningFailed(ctx, status, fmt.Sprintf("create schema: %v", err))
		return fmt.Errorf("%w: %w", ErrSchemaCreationFailed, err)
	}
	logger.Debug("schema created successfully")

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
// Returns nil status if already active (no-op), or error if provisioning cannot proceed.
func (p *PostgresProvisioner) prepareProvisioningStatus(ctx context.Context, tenantID organization.OrganizationID) (*ProvisioningStatus, error) {
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
func (p *PostgresProvisioner) provisionAllServices(ctx context.Context, status *ProvisioningStatus, schemaName string) error {
	logger := p.logger.With("tenant_id", status.TenantID.String(), "schema", schemaName)

	for i, svc := range p.config.Services {
		if ctx.Err() != nil {
			logger.Warn("provisioning timeout", "service", svc.Name)
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("timeout during %s migrations", svc.Name))
			return ErrProvisioningTimeout
		}

		logger.Debug("provisioning service", "service", svc.Name, "migration_path", svc.MigrationPath)

		// Update service status to created
		status.Services[i].State = ServiceStateCreated
		status.UpdatedAt = time.Now()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			logger.Warn("failed to save intermediate status", "error", err, "service", svc.Name)
		}

		// Apply migrations
		version, err := p.applyServiceMigrations(ctx, schemaName, svc)
		if err != nil {
			logger.Error("service migration failed", "service", svc.Name, "error", err)
			status.Services[i].State = ServiceStateFailed
			status.Services[i].ErrorMessage = err.Error()
			p.markProvisioningFailed(ctx, status, fmt.Sprintf("%s migrations failed: %v", svc.Name, err))
			return fmt.Errorf("%w: %s: %w", ErrMigrationFailed, svc.Name, err)
		}

		logger.Debug("service migration completed", "service", svc.Name, "version", version)
		status.Services[i].State = ServiceStateMigrated
		status.Services[i].MigrationVersion = version
		status.UpdatedAt = time.Now()
		if err := p.saveProvisioningStatus(ctx, status); err != nil {
			logger.Warn("failed to save migration status", "error", err, "service", svc.Name)
		}
	}
	return nil
}

// markProvisioningFailed updates status to failed state with error message.
func (p *PostgresProvisioner) markProvisioningFailed(ctx context.Context, status *ProvisioningStatus, errorMsg string) {
	status.State = StateFailed
	status.ErrorMessage = errorMsg
	status.UpdatedAt = time.Now()
	if err := p.saveProvisioningStatus(ctx, status); err != nil {
		p.logger.Warn("failed to save failed status",
			"tenant_id", status.TenantID.String(),
			"error", err)
	}
}

// DeprovisionSchemas marks the tenant schemas as deprovisioned (soft delete).
// The actual schema data remains in the database until explicitly purged.
func (p *PostgresProvisioner) DeprovisionSchemas(ctx context.Context, tenantID organization.OrganizationID) error {
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
		return fmt.Errorf("%w: %w", ErrDeprovisioningFailed, err)
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
func (p *PostgresProvisioner) PurgeSchemas(ctx context.Context, tenantID organization.OrganizationID) error {
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

	// Drop the schema
	schemaName := tenantID.SchemaName()
	if err := p.dropSchema(ctx, schemaName); err != nil {
		logger.Error("failed to drop schema", "error", err)
		return fmt.Errorf("%w: %w", ErrDeprovisioningFailed, err)
	}
	logger.Debug("schema dropped successfully")

	// Remove the provisioning record
	if err := p.deleteProvisioningStatus(ctx, tenantID); err != nil {
		logger.Error("failed to delete provisioning status", "error", err)
		return fmt.Errorf("delete provisioning status: %w", err)
	}

	logger.Info("schema purge completed")
	return nil
}

// GetProvisioningStatus retrieves the current provisioning state for a tenant.
func (p *PostgresProvisioner) GetProvisioningStatus(ctx context.Context, tenantID organization.OrganizationID) (*ProvisioningStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getProvisioningStatusLocked(ctx, tenantID)
}

// createSchema creates the org_{tenant_id} schema if it doesn't exist.
func (p *PostgresProvisioner) createSchema(ctx context.Context, schemaName string) error {
	// Use quote_ident for proper escaping
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quoteIdentifier(schemaName))
	return p.db.WithContext(ctx).Exec(query).Error
}

// dropSchema drops the org_{tenant_id} schema and all its contents.
func (p *PostgresProvisioner) dropSchema(ctx context.Context, schemaName string) error {
	query := fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quoteIdentifier(schemaName))
	return p.db.WithContext(ctx).Exec(query).Error
}

// applyServiceMigrations applies all migration files for a service to the tenant schema.
// Returns the version string of the last applied migration.
func (p *PostgresProvisioner) applyServiceMigrations(ctx context.Context, schemaName string, svc ServiceConfig) (string, error) {
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

		// Execute migration within a transaction
		err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Set search_path
			if err := tx.Exec(setPathQuery).Error; err != nil {
				return fmt.Errorf("set search_path: %w", err)
			}

			// Process the migration content to remove schema prefixes
			processedSQL := p.processMigrationSQL(mig.Content, schemaName)

			// Execute migration
			if err := tx.Exec(processedSQL).Error; err != nil {
				return fmt.Errorf("execute migration %s: %w", mig.Filename, err)
			}

			return nil
		})
		if err != nil {
			// Check if error is due to existing objects (idempotency)
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

// createInitialServiceStatuses creates the initial service status list.
func (p *PostgresProvisioner) createInitialServiceStatuses(tenantID organization.OrganizationID) []ServiceSchemaStatus {
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

func (provisioningEntity) TableName() string {
	return "platform.tenant_provisioning"
}

// getProvisioningStatusLocked retrieves the status without acquiring the mutex.
// Caller must hold the mutex.
func (p *PostgresProvisioner) getProvisioningStatusLocked(ctx context.Context, tenantID organization.OrganizationID) (*ProvisioningStatus, error) {
	var entity provisioningEntity
	result := p.db.WithContext(ctx).Where("tenant_id = ?", tenantID.String()).First(&entity)

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
	upsertSQL := `
		INSERT INTO platform.tenant_provisioning
			(tenant_id, state, service_schemas, error_message, created_at, updated_at, deprovisioned_at, version)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (tenant_id) DO UPDATE SET
			state = EXCLUDED.state,
			service_schemas = EXCLUDED.service_schemas,
			error_message = EXCLUDED.error_message,
			updated_at = EXCLUDED.updated_at,
			deprovisioned_at = EXCLUDED.deprovisioned_at,
			version = platform.tenant_provisioning.version + 1
	`

	return p.db.WithContext(ctx).Exec(
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
func (p *PostgresProvisioner) deleteProvisioningStatus(ctx context.Context, tenantID organization.OrganizationID) error {
	return p.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID.String()).
		Delete(&provisioningEntity{}).Error
}

// entityToStatus converts database entity to domain model.
func (p *PostgresProvisioner) entityToStatus(tenantID organization.OrganizationID, entity *provisioningEntity) (*ProvisioningStatus, error) {
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
// This is used for idempotency - if a table/index already exists, we skip it.
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

// Ensure PostgresProvisioner implements SchemaProvisioner.
var _ SchemaProvisioner = (*PostgresProvisioner)(nil)
