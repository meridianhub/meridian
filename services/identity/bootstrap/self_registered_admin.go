// Package bootstrap - self-registered admin provisioning hook.
//
// When a user self-registers via POST /api/v1/register and the tenant requires
// async provisioning, the registration handler stores the admin email and password
// hash in the tenant's metadata. This hook runs after schema provisioning completes
// and creates the self-registered admin identity in the now-available tenant schema.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// TenantMetadataStore provides read/write access to tenant metadata.
// Implemented by the tenant persistence repository.
type TenantMetadataStore interface {
	GetMetadata(ctx context.Context, id tenant.TenantID) (map[string]interface{}, error)
	UpdateMetadata(ctx context.Context, id tenant.TenantID, metadata map[string]interface{}) error
}

// Metadata keys for self-registered admin credentials stored on the tenant record.
// These MUST match the exported constants in services/api-gateway/registration_handler.go
// (MetaKeyRegistrationEmail, MetaKeyRegistrationPasswordHash). The test file verifies
// they stay in sync.
const (
	MetaKeyRegistrationEmail               = "_registration_email"
	MetaKeyRegistrationPasswordHash        = "_registration_password_hash"
	MetaKeyRegistrationEmailVerifyRequired = "_registration_email_verify_required"
)

var (
	// ErrNilTenantRepo is returned when a nil tenant repository is passed to NewSelfRegisteredAdminHook.
	ErrNilTenantRepo = errors.New("self-registered admin hook: tenant repository must not be nil")
	// ErrInvalidRegistrationMetadata is returned when registration metadata is malformed.
	ErrInvalidRegistrationMetadata = errors.New("self-registered admin hook: invalid registration metadata")
)

// SelfRegisteredAdminHook creates self-registered admin identities from tenant
// metadata after schema provisioning completes. It reads the admin email and
// password hash stored by the registration handler, creates the identity with
// tenant-owner role, and clears the credentials from metadata.
type SelfRegisteredAdminHook struct {
	identityRepo domain.Repository
	tenantStore  TenantMetadataStore
	logger       *slog.Logger
}

// NewSelfRegisteredAdminHook creates a new hook for provisioning self-registered admin identities.
func NewSelfRegisteredAdminHook(identityRepo domain.Repository, tenantStore TenantMetadataStore, logger *slog.Logger) (*SelfRegisteredAdminHook, error) {
	if identityRepo == nil {
		return nil, ErrNilRepository
	}
	if tenantStore == nil {
		return nil, ErrNilTenantRepo
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SelfRegisteredAdminHook{
		identityRepo: identityRepo,
		tenantStore:  tenantStore,
		logger:       logger,
	}, nil
}

// AsPostProvisioningHook returns a function compatible with the provisioning worker's
// PostProvisioningHook type.
//
// Usage:
//
//	hook, _ := bootstrap.NewSelfRegisteredAdminHook(identityRepo, tenantRepo, logger)
//	worker.RegisterPostProvisioningHook("self-registered-admin", hook.AsPostProvisioningHook())
func (h *SelfRegisteredAdminHook) AsPostProvisioningHook() func(ctx context.Context, tenantID tenant.TenantID) error {
	return h.Provision
}

// Provision creates the self-registered admin identity from tenant metadata.
// If no registration metadata is present (e.g., tenant was created via API, not
// self-registration), this is a no-op.
func (h *SelfRegisteredAdminHook) Provision(ctx context.Context, tenantID tenant.TenantID) error {
	// Read tenant metadata to check for registration credentials.
	metadata, err := h.tenantStore.GetMetadata(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("reading tenant metadata for %s: %w", tenantID, err)
	}

	emailRaw, hasEmail := metadata[MetaKeyRegistrationEmail]
	hashRaw, hasHash := metadata[MetaKeyRegistrationPasswordHash]

	// No registration metadata at all - not a self-registration, skip.
	if !hasEmail && !hasHash {
		h.logger.InfoContext(ctx, "self-registered admin hook: no registration metadata, skipping",
			"tenant_id", tenantID)
		return nil
	}

	// Partial metadata is an error - both keys must be present and valid.
	email, ok := emailRaw.(string)
	if !ok || email == "" {
		return fmt.Errorf("%w: missing or invalid email for tenant %s", ErrInvalidRegistrationMetadata, tenantID)
	}

	passwordHash, ok := hashRaw.(string)
	if !ok || passwordHash == "" {
		return fmt.Errorf("%w: missing or invalid password hash for tenant %s", ErrInvalidRegistrationMetadata, tenantID)
	}

	// Check if email verification is required (stored by registration handler).
	emailVerifyRequired, _ := metadata[MetaKeyRegistrationEmailVerifyRequired].(bool)

	// Create the identity in the tenant's schema.
	if err := h.createAdminIdentity(ctx, tenantID, email, passwordHash, emailVerifyRequired); err != nil {
		return fmt.Errorf("creating self-registered admin identity in tenant %s: %w", tenantID, err)
	}

	// Clear registration credentials from tenant metadata.
	// This is fatal: leaving a bcrypt hash in metadata violates minimal credential retention.
	if err := h.clearRegistrationMetadata(ctx, tenantID, metadata); err != nil {
		return fmt.Errorf("clearing registration metadata for tenant %s: %w", tenantID, err)
	}

	h.logger.InfoContext(ctx, "self-registered admin identity provisioned",
		"tenant_id", tenantID)
	return nil
}

// createAdminIdentity creates an identity with tenant-owner role.
// When emailVerifyRequired is true, the identity is created in PENDING_VERIFICATION state;
// otherwise it is activated immediately.
func (h *SelfRegisteredAdminHook) createAdminIdentity(ctx context.Context, tenantID tenant.TenantID, email, passwordHash string, emailVerifyRequired bool) error {
	tenantCtx := tenant.WithTenant(ctx, tenantID)

	// Check if identity already exists (idempotency).
	existing, err := h.identityRepo.FindByEmail(tenantCtx, email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing identity: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "self-registered admin already exists, skipping creation",
			"tenant_id", tenantID)
		return nil
	}

	var identity *domain.Identity
	if emailVerifyRequired {
		identity, err = domain.NewSelfRegisteredIdentity(tenantID, email, true)
	} else {
		identity, err = domain.NewIdentity(tenantID, email)
	}
	if err != nil {
		return fmt.Errorf("creating identity domain object: %w", err)
	}

	if err := identity.SetPassword(passwordHash); err != nil {
		return fmt.Errorf("setting password: %w", err)
	}

	if !emailVerifyRequired {
		if err := identity.Activate(); err != nil {
			return fmt.Errorf("activating identity: %w", err)
		}
	}

	now := time.Now()
	ra := domain.ReconstructRoleAssignment(
		uuid.New(),
		tenantID,
		identity.ID(),
		identity.ID(),
		domain.RoleTenantOwner,
		nil, nil, nil,
		now, now,
	)

	if err := h.identityRepo.SaveIdentityWithRoles(tenantCtx, identity, []*domain.RoleAssignment{ra}); err != nil {
		return fmt.Errorf("saving identity with roles: %w", err)
	}

	return nil
}

// clearRegistrationMetadata removes the registration credential keys from tenant metadata.
func (h *SelfRegisteredAdminHook) clearRegistrationMetadata(ctx context.Context, tenantID tenant.TenantID, metadata map[string]interface{}) error {
	// Copy metadata without registration keys.
	cleaned := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		if k == MetaKeyRegistrationEmail || k == MetaKeyRegistrationPasswordHash || k == MetaKeyRegistrationEmailVerifyRequired {
			continue
		}
		cleaned[k] = v
	}

	return h.tenantStore.UpdateMetadata(ctx, tenantID, cleaned)
}
