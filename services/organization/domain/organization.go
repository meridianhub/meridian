// Package domain contains the organization domain model for the Party Lifecycle Management service.
package domain

import (
	"time"

	"github.com/meridianhub/meridian/shared/platform/organization"
)

// Status represents the lifecycle state of an organization.
type Status string

const (
	// StatusActive means the organization is active and can operate.
	StatusActive Status = "active"
	// StatusSuspended means the organization is temporarily suspended.
	StatusSuspended Status = "suspended"
	// StatusDeprovisioned means the organization has been deprovisioned.
	StatusDeprovisioned Status = "deprovisioned"
)

// IsValid returns true if the status is a valid organization status.
func (s Status) IsValid() bool {
	switch s {
	case StatusActive, StatusSuspended, StatusDeprovisioned:
		return true
	default:
		return false
	}
}

// Organization represents a tenant in the BIAN Party Lifecycle Management domain.
// Organizations own their own data in isolated PostgreSQL schemas.
type Organization struct {
	// ID is the unique identifier (alphanumeric + underscore, 1-50 chars).
	// Used for schema routing (org_{id} schema) and API subdomain.
	ID organization.OrganizationID

	// DisplayName is the human-readable name of the organization.
	DisplayName string

	// SettlementAsset is the primary asset for this organization (e.g., GBP, USD, GPU-HOUR).
	SettlementAsset string

	// Subdomain is the API subdomain for this organization (e.g., acme-bank.demo.meridian.io).
	// Optional - not all organizations need a subdomain.
	Subdomain string

	// Status is the current lifecycle state of the organization.
	Status Status

	// CreatedAt is when the organization was registered.
	CreatedAt time.Time

	// DeprovisionedAt is when the organization was deprovisioned (nil if active/suspended).
	DeprovisionedAt *time.Time

	// Metadata contains flexible configuration (features, quotas, org-specific settings).
	Metadata map[string]interface{}

	// Version is for optimistic locking.
	Version int
}

// IsActive returns true if the organization is in active status.
func (o *Organization) IsActive() bool {
	return o.Status == StatusActive
}

// CanOperate returns true if the organization can perform operations.
// Only active organizations can operate.
func (o *Organization) CanOperate() bool {
	return o.Status == StatusActive
}

// SchemaName returns the PostgreSQL schema name for this organization's data.
// Uses the convention "org_" + lowercase(organization ID).
func (o *Organization) SchemaName() string {
	return o.ID.SchemaName()
}
