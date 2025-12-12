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
package provisioner

import (
	"context"
	"time"

	"github.com/meridianhub/meridian/shared/platform/organization"
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
	ProvisionSchemas(ctx context.Context, tenantID organization.OrganizationID) error

	// DeprovisionSchemas drops all org_{tenant_id} schemas and data.
	//
	// WARNING: This permanently deletes all tenant data. Use with caution.
	// Typically called only during:
	//  - Tenant deletion (after data export/archival)
	//  - Cleanup after failed provisioning
	//  - Development/testing
	//
	// The function performs:
	//  1. DROP SCHEMA org_{tenant_id} CASCADE (removes all objects)
	//  2. Removes provisioning status record
	//
	// Idempotency: Safe to call on non-existent schemas.
	DeprovisionSchemas(ctx context.Context, tenantID organization.OrganizationID) error

	// GetProvisioningStatus retrieves the current provisioning state for a tenant.
	//
	// Returns ErrProvisioningStatusNotFound if no provisioning record exists.
	// This can indicate the tenant was never provisioned or was fully deprovisioned.
	GetProvisioningStatus(ctx context.Context, tenantID organization.OrganizationID) (*ProvisioningStatus, error)
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
)

// IsValid returns true if the state is a recognized provisioning state.
func (s ProvisioningState) IsValid() bool {
	switch s {
	case StatePending, StateInProgress, StateActive, StateFailed:
		return true
	default:
		return false
	}
}

// IsTerminal returns true if the state is a final state (active or permanently failed).
func (s ProvisioningState) IsTerminal() bool {
	return s == StateActive || s == StateFailed
}

// ProvisioningStatus tracks the state of schema provisioning for a tenant.
type ProvisioningStatus struct {
	// TenantID is the organization identifier for this provisioning record.
	TenantID organization.OrganizationID

	// State is the current provisioning lifecycle state.
	State ProvisioningState

	// Services contains the provisioning status for each service's schema.
	// Key is the service name (e.g., "party", "current-account").
	Services []ServiceSchemaStatus

	// ErrorMessage contains details if State is StateFailed.
	// Empty string for successful provisioning.
	ErrorMessage string

	// CreatedAt is when provisioning was first initiated.
	CreatedAt time.Time

	// UpdatedAt is when the status was last modified.
	UpdatedAt time.Time
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
}

// Config holds configuration for the schema provisioner.
type Config struct {
	// Services lists the BIAN services that need schema provisioning.
	// Order matters: services are provisioned in the order listed.
	// Consider dependency order (e.g., party before current-account).
	Services []ServiceConfig

	// ProvisioningTimeout is the maximum time allowed for provisioning all schemas.
	// Default: 30 seconds.
	ProvisioningTimeout time.Duration
}

// DefaultConfig returns a configuration with standard BIAN services.
func DefaultConfig() *Config {
	return &Config{
		Services: []ServiceConfig{
			{Name: "party", MigrationPath: "services/party/migrations"},
			{Name: "current-account", MigrationPath: "services/current-account/migrations"},
			{Name: "position-keeping", MigrationPath: "services/position-keeping/migrations"},
			{Name: "financial-accounting", MigrationPath: "services/financial-accounting/migrations"},
			{Name: "payment-order", MigrationPath: "services/payment-order/migrations"},
		},
		ProvisioningTimeout: 30 * time.Second,
	}
}
