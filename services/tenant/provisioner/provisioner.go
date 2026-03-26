// Package provisioner defines interfaces and types for multi-tenant schema provisioning.
//
// Schema provisioning automates the creation of org_{tenant_id} PostgreSQL schemas
// when tenants are registered. Each tenant gets isolated database schemas for all
// BIAN services (party, current-account, position-keeping, etc.).
//
// Architecture Overview:
//
//	┌──────────────────────────────────────────────────────────────────────────┐
//	│                          InitiateTenant RPC                              │
//	└──────────────────────────┬───────────────────────────────────────────────┘
//	                           │
//	                           ▼
//	┌──────────────────────────────────────────────────────────────────────────┐
//	│                      SchemaProvisioner                                    │
//	│  ┌────────────────────────────────────────────────────────────────────┐  │
//	│  │  ProvisionSchemas(ctx, tenantID)                                   │  │
//	│  │    1. Create org_{tenant_id} schema                                │  │
//	│  │    2. Apply service migrations (party, current-account, etc.)      │  │
//	│  │    3. Update provisioning status                                   │  │
//	│  └────────────────────────────────────────────────────────────────────┘  │
//	└──────────────────────────────────────────────────────────────────────────┘
//	                           │
//	                           ▼
//	┌─────────────────┬────────────────┬─────────────────┬────────────────────┐
//	│   org_acme_bank │  org_beta_corp │  org_gamma_ltd  │       ...          │
//	│   ├─ parties    │  ├─ parties    │  ├─ parties     │                    │
//	│   ├─ accounts   │  ├─ accounts   │  ├─ accounts    │                    │
//	│   └─ positions  │  └─ positions  │  └─ positions   │                    │
//	└─────────────────┴────────────────┴─────────────────┴────────────────────┘
//
// Design Decisions:
//
//   - Synchronous provisioning: InitiateTenant blocks until schemas are ready
//     (simpler architecture, acceptable latency <5s for typical deployments)
//
//   - Service-specific migrations: Each service owns its schema template,
//     provisioner applies them to the new org schema
//
//   - Idempotency: CREATE SCHEMA IF NOT EXISTS, migration version tracking
//     allows safe retries after partial failures
//
//   - Rollback strategy: On failure, tenant is marked as provisioning_failed;
//     manual cleanup via DeprovisionSchemas or retry via ProvisionSchemas
//
//   - Audit trail: Provisioning records are NEVER hard deleted. Deprovisioning
//     marks the record as 'deprovisioned' with a timestamp for audit purposes.
//
//   - Data retention: Deprovisioned schemas are NOT automatically dropped.
//     A separate PurgeSchemas operation handles schema deletion after the
//     configured retention period (for regulatory compliance).
package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// SchemaProvisioner manages the lifecycle of tenant database schemas.
// Implementations must be safe for concurrent use.
type SchemaProvisioner interface {
	// ProvisionSchemas creates org_{tenant_id} schemas for all configured services.
	//
	// The function performs the following steps:
	//  1. Creates the org_{tenant_id} schema (CREATE SCHEMA IF NOT EXISTS)
	//  2. Applies baseline migrations for each configured service
	//  3. Updates provisioning status for tracking
	//
	// Idempotency: Safe to call multiple times; already-created schemas and
	// already-applied migrations are skipped.
	//
	// Returns an error if any service migration fails. Partial progress is
	// recorded in the provisioning status for debugging and retry.
	ProvisionSchemas(ctx context.Context, tenantID tenant.TenantID) error

	// DeprovisionSchemas marks tenant schemas as deprovisioned (soft delete).
	//
	// This is a SOFT DELETE operation for audit trail compliance:
	//  - Sets provisioning state to 'deprovisioned'
	//  - Records deprovisioned_at timestamp
	//  - Does NOT drop the actual database schema or data
	//
	// The org_{tenant_id} schema remains in the database until explicitly purged
	// via PurgeSchemas after the data retention period.
	//
	// Typically called during:
	//  - Tenant deactivation/offboarding
	//  - Account closure workflows
	//
	// Idempotency: Safe to call multiple times; already-deprovisioned tenants
	// are unchanged.
	DeprovisionSchemas(ctx context.Context, tenantID tenant.TenantID) error

	// PurgeSchemas permanently drops org_{tenant_id} schemas and all data.
	//
	// WARNING: This is a DESTRUCTIVE operation that permanently deletes all
	// tenant data. This action cannot be undone.
	//
	// Prerequisites:
	//  - Tenant must be in 'deprovisioned' state
	//  - Data retention period must have elapsed (checked by implementation)
	//  - Data export/archival should be completed before calling
	//
	// The function performs:
	//  1. Validates tenant is deprovisioned and retention period elapsed
	//  2. DROP SCHEMA org_{tenant_id} CASCADE (removes all objects)
	//  3. Removes the provisioning status record (purge completes the lifecycle)
	//
	// Returns ErrRetentionPeriodNotElapsed if called before retention period.
	// Returns ErrNotDeprovisioned if tenant is still active.
	PurgeSchemas(ctx context.Context, tenantID tenant.TenantID) error

	// GetProvisioningStatus retrieves the current provisioning state for a tenant.
	//
	// Returns ErrProvisioningStatusNotFound if no provisioning record exists.
	// Note: Deprovisioned tenants still have a status record (for audit trail).
	GetProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) (*ProvisioningStatus, error)

	// ReconcileMigrations applies any new migrations that have been added since
	// tenant provisioning for the specified tenant(s).
	//
	// When services add new migrations after tenants are created, existing tenant
	// schemas may not have these migrations applied. This method detects and applies
	// new migrations to bring tenant schemas up to date.
	//
	// Idempotency: Safe to call multiple times; already-applied migrations are skipped
	// based on the MigrationVersion recorded in ServiceSchemaStatus.
	//
	// Parameters:
	//   - tenantID: If non-nil, reconciles only that tenant. If nil, reconciles all
	//     active tenants.
	//
	// Returns:
	//   - reconciledCount: Number of tenants that had migrations applied
	//   - errors: Per-tenant errors (reconciliation continues despite individual failures)
	ReconcileMigrations(ctx context.Context, tenantID *tenant.TenantID) (reconciledCount int, errors []string)

	// GetRequiredSchemas returns the list of service names that require schema provisioning.
	// This is used to initialize provisioning_status records before async provisioning begins.
	GetRequiredSchemas() []string

	// InitializeProvisioningStatus creates an initial provisioning_status record with 'pending' state.
	// This is called during tenant creation to set up tracking records before async provisioning begins.
	// Idempotent: If a status record already exists, this is a no-op.
	InitializeProvisioningStatus(ctx context.Context, tenantID tenant.TenantID) error
}

// ProvisioningState represents the lifecycle state of schema provisioning.
type ProvisioningState string

const (
	// StatePending indicates provisioning has been requested but not started.
	StatePending ProvisioningState = "pending"

	// StateInProgress indicates provisioning is currently running.
	StateInProgress ProvisioningState = "in_progress"

	// StateActive indicates all schemas were successfully provisioned.
	StateActive ProvisioningState = "active"

	// StateFailed indicates provisioning failed. Check ErrorMessage for details.
	// Failed provisioning can be retried via ProvisionSchemas.
	StateFailed ProvisioningState = "failed"

	// StateDeprovisioned indicates the tenant has been deprovisioned (soft deleted).
	// The schema data still exists but is marked for eventual cleanup.
	// This is a terminal state - provisioning cannot be retried.
	StateDeprovisioned ProvisioningState = "deprovisioned"
)

// IsValid returns true if the state is a recognized provisioning state.
func (s ProvisioningState) IsValid() bool {
	switch s {
	case StatePending, StateInProgress, StateActive, StateFailed, StateDeprovisioned:
		return true
	default:
		return false
	}
}

// IsTerminal returns true if the state is a final state with no automatic transitions.
// Terminal states:
//   - StateActive: provisioning complete, schema is ready (can transition to deprovisioned)
//   - StateFailed: provisioning failed (can transition to active via ProvisionSchemas retry)
//   - StateDeprovisioned: soft deleted (permanent, no further transitions)
//
// Note: "terminal" refers to the automatic state machine, not immutability.
// Manual operations like retry (failed→active) or deprovision (active→deprovisioned) are allowed.
func (s ProvisioningState) IsTerminal() bool {
	return s == StateActive || s == StateFailed || s == StateDeprovisioned
}

// ProvisioningStatus tracks the state of schema provisioning for a tenant.
type ProvisioningStatus struct {
	// TenantID is the organization identifier for this provisioning record.
	TenantID tenant.TenantID

	// State is the current provisioning lifecycle state.
	State ProvisioningState

	// Services contains the provisioning status for each service's schema.
	// Ordered by provisioning sequence (same order as Config.Services).
	Services []ServiceSchemaStatus

	// ErrorMessage contains details if State is StateFailed.
	// Empty string for successful provisioning.
	ErrorMessage string

	// CreatedAt is when provisioning was first initiated.
	CreatedAt time.Time

	// UpdatedAt is when the status was last modified.
	UpdatedAt time.Time

	// DeprovisionedAt is when the tenant was marked as deprovisioned.
	// Nil if the tenant is still active. Used for data retention policy enforcement.
	DeprovisionedAt *time.Time
}

// getServiceStatus returns the status for a specific service by name.
// Returns nil if the service is not found in the status (e.g., service was added
// to config after this tenant was provisioned).
func (s *ProvisioningStatus) getServiceStatus(serviceName string) *ServiceSchemaStatus {
	for i := range s.Services {
		if s.Services[i].ServiceName == serviceName {
			return &s.Services[i]
		}
	}
	return nil
}

// ServiceSchemaStatus tracks provisioning progress for a single service's schema.
type ServiceSchemaStatus struct {
	// ServiceName identifies the BIAN service (e.g., "party", "current-account").
	ServiceName string

	// SchemaName is the PostgreSQL schema created (e.g., "org_acme_bank").
	SchemaName string

	// State indicates this service's provisioning status.
	State ServiceProvisioningState

	// MigrationVersion is the highest migration version applied.
	// Empty string if no migrations have been applied yet.
	MigrationVersion string

	// ErrorMessage contains details if State is ServiceStateFailed.
	ErrorMessage string
}

// ServiceProvisioningState represents the provisioning state for a single service.
type ServiceProvisioningState string

const (
	// ServiceStatePending indicates the service schema has not been created yet.
	ServiceStatePending ServiceProvisioningState = "pending"

	// ServiceStateCreated indicates the schema exists but migrations are pending.
	ServiceStateCreated ServiceProvisioningState = "created"

	// ServiceStateMigrated indicates all migrations have been successfully applied.
	ServiceStateMigrated ServiceProvisioningState = "migrated"

	// ServiceStateFailed indicates migration failed for this service.
	ServiceStateFailed ServiceProvisioningState = "failed"

	// ServiceStateCircuitOpen indicates the circuit breaker is open for this service.
	// The service was skipped during provisioning because the circuit breaker
	// detected too many recent failures. Provisioning will be retried automatically
	// after the circuit breaker timeout period (default: 5 minutes).
	ServiceStateCircuitOpen ServiceProvisioningState = "circuit_open"
)

// ServiceConfig defines a service that needs schema provisioning.
// Used to configure the provisioner with the list of services to provision.
type ServiceConfig struct {
	// Name is the service identifier (e.g., "party", "current-account").
	// Used for logging and status tracking.
	Name string

	// MigrationPath is the path to the service's migration files.
	// These migrations use unqualified table names (no schema prefix).
	MigrationPath string

	// DatabaseURL is the connection string for this service's database.
	// In database-per-service architecture, each service has its own database.
	// The provisioner will create org_{tenant_id} schemas in this database.
	//
	// If empty, falls back to a constructed URL based on service name:
	// postgres://meridian_{service}_user@cockroachdb:26257/meridian_{service}?sslmode=disable
	DatabaseURL string
}

// PostProvisioningHook is called after successful schema provisioning for a tenant.
// It receives the tenant ID and should return an error if the hook fails.
// Hook failures are logged but do NOT fail the overall provisioning.
type PostProvisioningHook func(ctx context.Context, tenantID tenant.TenantID) error

// Config holds configuration for the schema provisioner.
type Config struct {
	// Services lists the BIAN services that need schema provisioning.
	// Order matters: services are provisioned in the order listed.
	// Consider dependency order (e.g., party before current-account).
	Services []ServiceConfig

	// ProvisioningTimeout is the maximum time allowed for provisioning all schemas.
	// Default: 30 seconds.
	ProvisioningTimeout time.Duration

	// DataRetentionPeriod is the minimum time that must elapse after deprovisioning
	// before schema data can be purged. This ensures compliance with data retention
	// regulations (e.g., financial record keeping requirements).
	// Default: 7 years (2555 days) for financial services compliance.
	DataRetentionPeriod time.Duration

	// PostProvisioningHooks are called after successful schema provisioning.
	// Hook failures are logged but do NOT fail the overall provisioning.
	// Use for seeding default data, configuring services, etc.
	PostProvisioningHooks []PostProvisioningHook
}

// Validate checks that the configuration is valid.
// Returns an error describing the first invalid field found.
func (c *Config) Validate() error {
	if len(c.Services) == 0 {
		return ErrNoServicesConfigured
	}
	if c.ProvisioningTimeout <= 0 {
		return ErrInvalidProvisioningTimeout
	}
	if c.DataRetentionPeriod < 0 {
		return ErrInvalidRetentionPeriod
	}
	for _, svc := range c.Services {
		if svc.Name == "" {
			return ErrEmptyServiceName
		}
		if svc.MigrationPath == "" {
			return fmt.Errorf("%w: %s", ErrEmptyMigrationPath, svc.Name)
		}
		// Note: DatabaseURL is intentionally not validated here.
		// Empty DatabaseURL is valid because getServiceDatabaseURL() provides
		// fallback URLs based on service name for backward compatibility.
	}
	return nil
}

// DefaultConfig returns a configuration with standard BIAN services.
// Migration paths default to /migrations/* for container deployment.
// Override via environment variable MIGRATIONS_BASE_PATH for local development.
//
// Database URLs are constructed from environment variables:
//   - PARTY_DATABASE_URL
//   - CURRENT_ACCOUNT_DATABASE_URL
//   - POSITION_KEEPING_DATABASE_URL
//   - FINANCIAL_ACCOUNTING_DATABASE_URL
//   - PAYMENT_ORDER_DATABASE_URL
//   - MARKET_INFORMATION_DATABASE_URL
//   - REFERENCE_DATA_DATABASE_URL
//
// If not set, fallback URLs are constructed based on service name.
func DefaultConfig() *Config {
	basePath := os.Getenv("MIGRATIONS_BASE_PATH")
	if basePath == "" {
		basePath = "/migrations" // Default for container deployment
	}

	return &Config{
		Services: []ServiceConfig{
			{
				Name:          "party",
				MigrationPath: basePath + "/party",
				DatabaseURL:   getServiceDatabaseURL("party"),
			},
			{
				Name:          "current-account",
				MigrationPath: basePath + "/current-account",
				DatabaseURL:   getServiceDatabaseURL("current-account"),
			},
			{
				Name:          "position-keeping",
				MigrationPath: basePath + "/position-keeping",
				DatabaseURL:   getServiceDatabaseURL("position-keeping"),
			},
			{
				Name:          "financial-accounting",
				MigrationPath: basePath + "/financial-accounting",
				DatabaseURL:   getServiceDatabaseURL("financial-accounting"),
			},
			{
				Name:          "payment-order",
				MigrationPath: basePath + "/payment-order",
				DatabaseURL:   getServiceDatabaseURL("payment-order"),
			},
			{
				Name:          "market-information",
				MigrationPath: basePath + "/market-information",
				DatabaseURL:   getServiceDatabaseURL("market-information"),
			},
			{
				Name:          "reference-data",
				MigrationPath: basePath + "/reference-data",
				DatabaseURL:   getServiceDatabaseURL("reference-data"),
			},
			// Services below use tenant-scoped queries (WithGormTenantScope)
			// and need org_<tenant> schemas, but have no provisioner-specific
			// migrations - only the schema needs to exist.
			{
				Name:          "internal-account",
				MigrationPath: basePath + "/internal-account",
				DatabaseURL:   getServiceDatabaseURL("internal-account"),
			},
			{
				Name:          "reconciliation",
				MigrationPath: basePath + "/reconciliation",
				DatabaseURL:   getServiceDatabaseURL("reconciliation"),
			},
			{
				Name:          "identity",
				MigrationPath: basePath + "/identity",
				DatabaseURL:   getServiceDatabaseURL("identity"),
			},
		},
		ProvisioningTimeout: defaults.DefaultRPCTimeout,
		DataRetentionPeriod: 7 * 365 * 24 * time.Hour, // 7 years
	}
}

// getServiceDatabaseURL constructs database URL from environment variables.
// Pattern: {SERVICE_NAME}_DATABASE_URL (uppercase with hyphens replaced by underscores)
// Example: PARTY_DATABASE_URL, CURRENT_ACCOUNT_DATABASE_URL
//
// If the environment variable is not set, falls back to a constructed URL:
// postgres://meridian_{service}_user@cockroachdb:26257/meridian_{service}?sslmode=disable
func getServiceDatabaseURL(serviceName string) string {
	envKey := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_")) + "_DATABASE_URL"
	url := os.Getenv(envKey)
	if url != "" {
		return url
	}

	// Fallback to constructed URL for backward compatibility.
	// WARNING: Fallback URLs use sslmode=disable which is only suitable for local dev.
	// In production, always set explicit DATABASE_URL environment variables with
	// appropriate SSL settings (e.g., sslmode=verify-full).
	slog.Warn("using fallback database URL (not recommended for production)",
		"service", serviceName,
		"env_var", envKey,
		"hint", "Set "+envKey+" environment variable with appropriate SSL settings")
	dbName := "meridian_" + strings.ReplaceAll(serviceName, "-", "_")
	user := dbName + "_user"
	return fmt.Sprintf("postgres://%s@cockroachdb:26257/%s?sslmode=disable", user, dbName)
}
