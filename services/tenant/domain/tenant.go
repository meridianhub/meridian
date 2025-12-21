// Package domain contains the tenant domain model for the platform Tenant Lifecycle Management service.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Status represents the lifecycle state of a tenant.
type Status string

const (
	// StatusProvisioningPending means the tenant registration is queued for async provisioning.
	StatusProvisioningPending Status = "provisioning_pending"
	// StatusProvisioning means the tenant is being provisioned (schemas being created).
	StatusProvisioning Status = "provisioning"
	// StatusProvisioningFailed means schema provisioning failed.
	StatusProvisioningFailed Status = "provisioning_failed"
	// StatusActive means the tenant is active and can operate.
	StatusActive Status = "active"
	// StatusSuspended means the tenant is temporarily suspended.
	StatusSuspended Status = "suspended"
	// StatusDeprovisioned means the tenant has been deprovisioned.
	StatusDeprovisioned Status = "deprovisioned"
)

// IsValid returns true if the status is a valid tenant status.
func (s Status) IsValid() bool {
	switch s {
	case StatusProvisioningPending, StatusProvisioning, StatusProvisioningFailed, StatusActive, StatusSuspended, StatusDeprovisioned:
		return true
	default:
		return false
	}
}

// Tenant represents a platform tenant for multi-tenant infrastructure.
// Tenants own their own data in isolated PostgreSQL schemas.
// Note: This is distinct from BIAN Party.Organization which represents legal entities.
type Tenant struct {
	// ID is the unique identifier (alphanumeric + underscore, 1-50 chars).
	// Used for schema routing (org_{id} schema) and API subdomain.
	ID tenant.TenantID

	// Slug is the URL-friendly unique identifier (lowercase alphanumeric + hyphens).
	// Used for user-facing URLs and subdomain routing.
	Slug string

	// DisplayName is the human-readable name of the tenant.
	DisplayName string

	// SettlementAsset is the primary asset for this tenant (e.g., GBP, USD, GPU-HOUR).
	SettlementAsset string

	// Subdomain is the API subdomain for this tenant (e.g., acme-bank.demo.meridian.io).
	// Optional - not all tenants need a subdomain.
	Subdomain string

	// Status is the current lifecycle state of the tenant.
	Status Status

	// CreatedAt is when the tenant was registered.
	CreatedAt time.Time

	// DeprovisionedAt is when the tenant was deprovisioned (nil if active/suspended).
	DeprovisionedAt *time.Time

	// Metadata contains flexible configuration (features, quotas, tenant-specific settings).
	Metadata map[string]interface{}

	// Version is for optimistic locking.
	Version int

	// PartyID is the reference to the corresponding Party in the BIAN Party Reference Data Directory.
	// Automatically populated when the tenant is created via PartyService.RegisterParty.
	// This links platform infrastructure (Tenant) to BIAN domain entities (Party.Organization).
	PartyID string

	// ErrorMessage contains details if Status is provisioning_failed.
	// Empty string for successfully provisioned tenants.
	ErrorMessage string
}

// IsActive returns true if the tenant is in active status.
func (t *Tenant) IsActive() bool {
	return t.Status == StatusActive
}

// CanOperate returns true if the tenant can perform operations.
// Only active tenants can operate.
func (t *Tenant) CanOperate() bool {
	return t.Status == StatusActive
}

// SchemaName returns the PostgreSQL schema name for this tenant's data.
// Uses the convention "org_" + lowercase(tenant ID).
func (t *Tenant) SchemaName() string {
	return t.ID.SchemaName()
}

// CanTransitionTo returns true if the tenant can transition to the given status.
// Valid transitions:
//   - provisioning_pending → provisioning, provisioning_failed
//   - provisioning → active, provisioning_failed
//   - provisioning_failed → provisioning (retry)
//   - active → suspended, deprovisioned
//   - suspended → active, deprovisioned
//   - deprovisioned → (none, terminal state)
func (t *Tenant) CanTransitionTo(newStatus Status) bool {
	if t.Status == newStatus {
		return true // No-op transitions are allowed
	}

	switch t.Status {
	case StatusProvisioningPending:
		return newStatus == StatusProvisioning || newStatus == StatusProvisioningFailed
	case StatusProvisioning:
		return newStatus == StatusActive || newStatus == StatusProvisioningFailed
	case StatusProvisioningFailed:
		return newStatus == StatusProvisioning // Allow retry
	case StatusActive:
		return newStatus == StatusSuspended || newStatus == StatusDeprovisioned
	case StatusSuspended:
		return newStatus == StatusActive || newStatus == StatusDeprovisioned
	case StatusDeprovisioned:
		return false // Deprovisioned is a terminal state
	default:
		return false
	}
}

var (
	// slugPattern enforces lowercase alphanumeric with hyphens, no leading/trailing hyphens.
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

	// reservedSlugs contains system-reserved slugs that cannot be used for tenants.
	reservedSlugs = map[string]bool{
		"api":      true,
		"health":   true,
		"admin":    true,
		"www":      true,
		"status":   true,
		"docs":     true,
		"internal": true,
		"system":   true,
		"platform": true,
	}

	// ErrSlugTooShort is returned when a slug is shorter than the minimum required length.
	ErrSlugTooShort = errors.New("slug must be at least 3 characters long")
	// ErrSlugTooLong is returned when a slug exceeds the maximum allowed length.
	ErrSlugTooLong = errors.New("slug must be at most 63 characters long")
	// ErrSlugInvalidFormat is returned when a slug contains invalid characters or format.
	ErrSlugInvalidFormat = errors.New("slug must contain only lowercase alphanumeric characters and hyphens, and cannot start or end with a hyphen")
	// ErrSlugReserved is returned when a slug matches a system-reserved word.
	ErrSlugReserved = errors.New("slug is reserved and cannot be used")
)

// ValidateSlug validates a tenant slug according to platform constraints.
// Returns nil for empty slug (optional field).
// Validation rules:
//   - Length: 3-63 characters
//   - Format: lowercase alphanumeric with hyphens, no leading/trailing hyphens
//   - Reserved words: api, health, admin, www, status, docs, internal, system, platform
func ValidateSlug(slug string) error {
	// Empty slug is valid (optional field)
	if slug == "" {
		return nil
	}

	// Check length constraints
	if len(slug) < 3 {
		return fmt.Errorf("%w, got %d", ErrSlugTooShort, len(slug))
	}
	if len(slug) > 63 {
		return fmt.Errorf("%w, got %d", ErrSlugTooLong, len(slug))
	}

	// Check format with regex
	if !slugPattern.MatchString(slug) {
		return ErrSlugInvalidFormat
	}

	// Check reserved words
	if reservedSlugs[slug] {
		return fmt.Errorf("%w: '%s'", ErrSlugReserved, slug)
	}

	return nil
}
