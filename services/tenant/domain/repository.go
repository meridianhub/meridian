// Package domain contains the tenant domain model for the platform Tenant Lifecycle Management service.
package domain

import (
	"context"
	"errors"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Repository errors.
var (
	// ErrNotFound is the domain-layer error for tenant not found scenarios.
	// Note: Persistence implementations may return their own specific errors
	// (e.g., persistence.ErrTenantNotFound). Callers should check both or
	// use the persistence layer errors directly when available.
	ErrNotFound = errors.New("tenant not found")
)

// TenantRepository defines the contract for persisting and retrieving Tenant aggregates.
//
// All methods accept a context for cancellation and deadline control.
// The repository implementation should handle database transactions appropriately.
type TenantRepository interface {
	// Create persists a new Tenant to the database.
	// Returns an error if a tenant with the same ID, slug, or subdomain already exists.
	Create(ctx context.Context, tenant *Tenant) error

	// GetByID retrieves a Tenant by its unique identifier.
	// Returns a not-found error if the tenant doesn't exist.
	GetByID(ctx context.Context, id tenant.TenantID) (*Tenant, error)

	// GetBySlug retrieves a Tenant by its URL-friendly slug identifier.
	// Uses an indexed lookup for fast resolution.
	// Returns a not-found error if no tenant with the given slug exists.
	GetBySlug(ctx context.Context, slug string) (*Tenant, error)

	// IsSlugAvailable checks if a slug is available for registration.
	// Returns true if the slug is not in use, false if it's already taken.
	// Returns an error only if the database query fails.
	IsSlugAvailable(ctx context.Context, slug string) (bool, error)

	// IsActive checks if a tenant exists and is active.
	// This is optimized for validation middleware - returns only what's needed.
	// Returns a not-found error if the tenant doesn't exist.
	IsActive(ctx context.Context, id tenant.TenantID) (bool, error)

	// UpdateStatus changes the tenant status with optimistic locking.
	// Returns a not-found error if the tenant doesn't exist.
	// Returns a version conflict error if the tenant was modified by another transaction.
	UpdateStatus(ctx context.Context, id tenant.TenantID, status Status, currentVersion int) (*Tenant, error)

	// UpdateStatusWithError changes the tenant status and sets an error message.
	// Used for recording provisioning failures.
	UpdateStatusWithError(ctx context.Context, id tenant.TenantID, status Status, errorMessage string, currentVersion int) (*Tenant, error)

	// List returns tenants with optional status filter and pagination.
	// Returns the matching tenants, a next page token (if more results exist), and any error.
	List(ctx context.Context, statusFilter *Status, pageSize int, pageToken string) ([]*Tenant, string, error)

	// GetAll returns all tenants (for cache initialization).
	GetAll(ctx context.Context) ([]*Tenant, error)
}
