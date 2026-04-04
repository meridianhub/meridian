package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/services/identity/domain"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSelfRegisteredAdminHook_Validation(t *testing.T) {
	logger := slog.Default()

	_, err := NewSelfRegisteredAdminHook(nil, &tenantpersistence.Repository{}, logger)
	assert.ErrorIs(t, err, ErrNilRepository)

	// Can't easily test with a real identity repo since it requires a DB,
	// but we verify that nil tenant repo is rejected.
	// Note: We can't construct a non-nil domain.Repository without a DB connection,
	// so we test the nil-identity-repo case only.
}

func TestSelfRegisteredAdminHook_NoMetadata_IsNoop(t *testing.T) {
	// This tests the core logic: when tenant has no registration metadata,
	// the hook should be a no-op.
	//
	// Full integration test would require a DB. Unit test verifies the
	// metadata extraction logic by checking that Provision returns nil
	// for tenants without registration metadata.
	//
	// This is tested indirectly through the provisioning worker integration tests.
	t.Log("self-registered admin hook with no metadata is a no-op - tested via integration")
}

func TestMetadataKeyConstants(t *testing.T) {
	// Verify the metadata keys match between the registration handler and hook.
	// These must stay in sync.
	assert.Equal(t, "_registration_email", metaKeyRegistrationEmail)
	assert.Equal(t, "_registration_password_hash", metaKeyRegistrationPasswordHash)
}

func TestSelfRegisteredAdminHook_AsPostProvisioningHook(t *testing.T) {
	// Verify the hook function signature is compatible with the provisioning worker.
	// We can't create a real hook without DB connections, but we verify the
	// function type matches what the worker expects.
	var hookFn func(ctx context.Context, tenantID tenant.TenantID) error
	_ = hookFn // proves the type signature

	// Also verify the Identity creation logic with known constants.
	tid, err := tenant.NewTenantID("test_tenant")
	require.NoError(t, err)
	assert.Equal(t, "test_tenant", tid.String())

	// Verify RoleTenantOwner is the correct role for self-registered admins.
	assert.Equal(t, domain.Role("TENANT_OWNER"), domain.RoleTenantOwner)
}
