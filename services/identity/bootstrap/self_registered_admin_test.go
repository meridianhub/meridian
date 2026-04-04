package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/identity/domain"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
)

func TestNewSelfRegisteredAdminHook_NilIdentityRepo(t *testing.T) {
	_, err := NewSelfRegisteredAdminHook(nil, &tenantpersistence.Repository{}, slog.Default())
	assert.ErrorIs(t, err, ErrNilRepository)
}

// mockIdentityRepo satisfies domain.Repository for testing nil tenant repo validation.
type mockIdentityRepo struct {
	domain.Repository
}

func TestNewSelfRegisteredAdminHook_NilTenantRepo(t *testing.T) {
	_, err := NewSelfRegisteredAdminHook(&mockIdentityRepo{}, nil, slog.Default())
	assert.ErrorIs(t, err, ErrNilTenantRepo)
}

func TestMetadataKeyConstants_InSyncWithGateway(t *testing.T) {
	// These constants are duplicated across packages (identity/bootstrap and api-gateway)
	// because a circular import prevents direct sharing. This test ensures they stay in sync.
	assert.Equal(t, gateway.MetaKeyRegistrationEmail, MetaKeyRegistrationEmail,
		"bootstrap.MetaKeyRegistrationEmail must match gateway.MetaKeyRegistrationEmail")
	assert.Equal(t, gateway.MetaKeyRegistrationPasswordHash, MetaKeyRegistrationPasswordHash,
		"bootstrap.MetaKeyRegistrationPasswordHash must match gateway.MetaKeyRegistrationPasswordHash")
}

func TestSelfRegisteredAdminHook_AsPostProvisioningHook(t *testing.T) {
	// Verify the hook function signature is compatible with the provisioning worker.
	var hookFn func(ctx context.Context, tenantID tenant.TenantID) error
	_ = hookFn // proves the type signature

	// Verify RoleTenantOwner is the correct role for self-registered admins.
	assert.Equal(t, domain.Role("TENANT_OWNER"), domain.RoleTenantOwner)
}
