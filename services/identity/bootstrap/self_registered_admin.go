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
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// Metadata keys matching the registration handler constants.
const (
	metaKeyRegistrationEmail        = "_registration_email"
	metaKeyRegistrationPasswordHash = "_registration_password_hash"
)

// ErrNilTenantRepo is returned when a nil tenant repository is passed to NewSelfRegisteredAdminHook.
var ErrNilTenantRepo = errors.New("self-registered admin hook: tenant repository must not be nil")

// SelfRegisteredAdminHook creates self-registered admin identities from tenant
// metadata after schema provisioning completes. It reads the admin email and
// password hash stored by the registration handler, creates the identity with
// tenant-owner role, and clears the credentials from metadata.
type SelfRegisteredAdminHook struct {
	identityRepo domain.Repository
	tenantRepo   *tenantpersistence.Repository
	logger       *slog.Logger
}

// NewSelfRegisteredAdminHook creates a new hook for provisioning self-registered admin identities.
func NewSelfRegisteredAdminHook(identityRepo domain.Repository, tenantRepo *tenantpersistence.Repository, logger *slog.Logger) (*SelfRegisteredAdminHook, error) {
	if identityRepo == nil {
		return nil, ErrNilRepository
	}
	if tenantRepo == nil {
		return nil, ErrNilTenantRepo
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SelfRegisteredAdminHook{
		identityRepo: identityRepo,
		tenantRepo:   tenantRepo,
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
	// Read tenant to get registration metadata.
	t, err := h.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("reading tenant %s: %w", tenantID, err)
	}

	emailRaw, hasEmail := t.Metadata[metaKeyRegistrationEmail]
	hashRaw, hasHash := t.Metadata[metaKeyRegistrationPasswordHash]

	if !hasEmail || !hasHash {
		h.logger.InfoContext(ctx, "self-registered admin hook: no registration metadata, skipping",
			"tenant_id", tenantID)
		return nil
	}

	email, ok := emailRaw.(string)
	if !ok || email == "" {
		h.logger.WarnContext(ctx, "self-registered admin hook: invalid email in metadata, skipping",
			"tenant_id", tenantID)
		return nil
	}

	passwordHash, ok := hashRaw.(string)
	if !ok || passwordHash == "" {
		h.logger.WarnContext(ctx, "self-registered admin hook: invalid password hash in metadata, skipping",
			"tenant_id", tenantID)
		return nil
	}

	// Create the identity in the tenant's schema.
	if err := h.createAdminIdentity(ctx, tenantID, email, passwordHash); err != nil {
		return fmt.Errorf("creating self-registered admin identity in tenant %s: %w", tenantID, err)
	}

	// Clear registration credentials from tenant metadata.
	if err := h.clearRegistrationMetadata(ctx, tenantID, t.Metadata); err != nil {
		// Non-fatal: identity was created successfully. Log and continue.
		h.logger.WarnContext(ctx, "self-registered admin hook: failed to clear registration metadata",
			"tenant_id", tenantID,
			"error", err)
	}

	h.logger.InfoContext(ctx, "self-registered admin identity provisioned",
		"tenant_id", tenantID,
		"email", email)
	return nil
}

// createAdminIdentity creates an active identity with tenant-owner role.
func (h *SelfRegisteredAdminHook) createAdminIdentity(ctx context.Context, tenantID tenant.TenantID, email, passwordHash string) error {
	tenantCtx := tenant.WithTenant(ctx, tenantID)

	// Check if identity already exists (idempotency).
	existing, err := h.identityRepo.FindByEmail(tenantCtx, email)
	if err != nil && !errors.Is(err, domain.ErrIdentityNotFound) {
		return fmt.Errorf("checking for existing identity: %w", err)
	}
	if existing != nil {
		h.logger.InfoContext(ctx, "self-registered admin already exists, skipping creation",
			"tenant_id", tenantID,
			"email", email)
		return nil
	}

	identity, err := domain.NewIdentity(tenantID, email)
	if err != nil {
		return fmt.Errorf("creating identity domain object: %w", err)
	}

	if err := identity.SetPassword(passwordHash); err != nil {
		return fmt.Errorf("setting password: %w", err)
	}

	if err := identity.Activate(); err != nil {
		return fmt.Errorf("activating identity: %w", err)
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
		if k == metaKeyRegistrationEmail || k == metaKeyRegistrationPasswordHash {
			continue
		}
		cleaned[k] = v
	}

	return h.tenantRepo.UpdateMetadata(ctx, tenantID, cleaned)
}
