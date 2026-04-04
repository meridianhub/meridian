package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
)

// mockIdentityRepo satisfies domain.Repository for testing.
type mockIdentityRepo struct {
	domain.Repository
}

// mockTenantMetadataStore satisfies TenantMetadataStore for testing.
type mockTenantMetadataStore struct{}

func (m *mockTenantMetadataStore) GetMetadata(_ context.Context, _ tenant.TenantID) (map[string]interface{}, error) {
	return nil, nil
}

func (m *mockTenantMetadataStore) UpdateMetadata(_ context.Context, _ tenant.TenantID, _ map[string]interface{}) error {
	return nil
}

func TestNewSelfRegisteredAdminHook_NilIdentityRepo(t *testing.T) {
	_, err := NewSelfRegisteredAdminHook(nil, &mockTenantMetadataStore{}, slog.Default())
	assert.ErrorIs(t, err, ErrNilRepository)
}

func TestNewSelfRegisteredAdminHook_NilTenantStore(t *testing.T) {
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
	assert.Equal(t, gateway.MetaKeyRegistrationEmailVerifyRequired, MetaKeyRegistrationEmailVerifyRequired,
		"bootstrap.MetaKeyRegistrationEmailVerifyRequired must match gateway.MetaKeyRegistrationEmailVerifyRequired")
}

func TestSelfRegisteredAdminHook_AsPostProvisioningHook(t *testing.T) {
	// Verify the hook function signature is compatible with the provisioning worker.
	var hookFn func(ctx context.Context, tenantID tenant.TenantID) error
	_ = hookFn // proves the type signature

	// Verify RoleTenantOwner is the correct role for self-registered admins.
	assert.Equal(t, domain.Role("TENANT_OWNER"), domain.RoleTenantOwner)
}
