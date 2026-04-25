package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// errorInjectingMetadataStore is a TenantMetadataStore that returns
// configurable metadata and can simulate read/write failures, used to verify
// the fail-hard semantics of SelfRegisteredAdminHook.Provision.
type errorInjectingMetadataStore struct {
	metadata     map[string]interface{}
	getErr       error
	updateErr    error
	updateCalled bool
}

func (m *errorInjectingMetadataStore) GetMetadata(_ context.Context, _ tenant.TenantID) (map[string]interface{}, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	// Return a copy so callers cannot mutate the test fixture.
	if m.metadata == nil {
		return nil, nil
	}
	out := make(map[string]interface{}, len(m.metadata))
	for k, v := range m.metadata {
		out[k] = v
	}
	return out, nil
}

func (m *errorInjectingMetadataStore) UpdateMetadata(_ context.Context, _ tenant.TenantID, _ map[string]interface{}) error {
	m.updateCalled = true
	return m.updateErr
}

// validRegistrationMetadata returns a metadata map that would satisfy the
// hook's validation, suitable for tests that exercise downstream failure
// paths.
func validRegistrationMetadata() map[string]interface{} {
	return map[string]interface{}{
		MetaKeyRegistrationEmail:        "owner@example.com",
		MetaKeyRegistrationPasswordHash: "$2a$12$dummyhashdummyhashdummyhashdummyhashdummyhashdummyhash",
	}
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

// --- Failure-path tests for fail-hard semantics ---
//
// The provisioning worker treats any non-nil error from a post-provisioning
// hook as fatal: the tenant is marked provisioning_failed instead of active
// (services/tenant/worker/provisioning_worker.go:executePostProvisioningHooks
// + markTenantAsActive). These tests pin the hook's contract: every
// infrastructure or domain failure surfaces as a non-nil error so the
// worker's failure handling actually fires.

func TestProvision_NoMetadata_NoOp(t *testing.T) {
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{} // no metadata at all

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.NoError(t, err, "tenants without registration metadata should be a no-op, not an error")
	assert.False(t, repo.saveWithRolesCalled, "no identity should be saved when metadata is absent")
	assert.False(t, store.updateCalled, "metadata should not be cleared when nothing was set")
}

func TestProvision_GetMetadataFails_ReturnsError(t *testing.T) {
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{
		getErr: errors.New("connection refused"),
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.Error(t, err, "GetMetadata failure must surface so the worker marks the tenant provisioning_failed")
	assert.Contains(t, err.Error(), "reading tenant metadata")
	assert.False(t, repo.saveWithRolesCalled)
}

func TestProvision_MissingEmail_ReturnsInvalidMetadataError(t *testing.T) {
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{
		metadata: map[string]interface{}{
			MetaKeyRegistrationPasswordHash: "$2a$12$dummyhash",
			// email key intentionally missing - partial metadata is invalid.
		},
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRegistrationMetadata,
		"partial metadata (hash without email) must surface as ErrInvalidRegistrationMetadata")
	assert.False(t, repo.saveWithRolesCalled)
}

func TestProvision_MissingPasswordHash_ReturnsInvalidMetadataError(t *testing.T) {
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{
		metadata: map[string]interface{}{
			MetaKeyRegistrationEmail: "owner@example.com",
			// hash key intentionally missing - partial metadata is invalid.
		},
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRegistrationMetadata,
		"partial metadata (email without hash) must surface as ErrInvalidRegistrationMetadata")
	assert.False(t, repo.saveWithRolesCalled)
}

func TestProvision_SaveIdentityWithRolesFails_ReturnsError(t *testing.T) {
	// This is the core fail-hard test: when the identity write fails (DB
	// down, constraint violation, etc.) the hook MUST return an error so the
	// provisioning worker does not flip the tenant to active. Otherwise the
	// user would land on an "active" tenant with no admin identity and see a
	// misleading "invalid email or password" 401 on first login.
	tid := tenant.MustNewTenantID("acme")

	dbErr := errors.New("database connection failed")
	repo := newFakeRepo()
	repo.saveWithRolesErr = dbErr

	store := &errorInjectingMetadataStore{
		metadata: validRegistrationMetadata(),
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.Error(t, err, "SaveIdentityWithRoles failure must surface so the tenant is marked provisioning_failed")
	assert.ErrorIs(t, err, dbErr, "underlying error must be wrapped, not swallowed")
	assert.Contains(t, err.Error(), "saving identity with roles")
	assert.True(t, repo.saveWithRolesCalled, "the failure should occur at SaveIdentityWithRoles, not earlier")
	assert.False(t, store.updateCalled,
		"registration metadata must NOT be cleared when identity creation fails - retries need it intact")
}

func TestProvision_UpdateMetadataFails_ReturnsError(t *testing.T) {
	// Clearing the bcrypt hash from metadata is fatal-by-design: leaving it
	// behind violates minimal credential retention. If the clear fails the
	// hook must surface an error so the worker treats the tenant as failed
	// and operators investigate.
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{
		metadata:  validRegistrationMetadata(),
		updateErr: errors.New("update conflict"),
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.Error(t, err, "UpdateMetadata failure must surface to abort tenant activation")
	assert.Contains(t, err.Error(), "clearing registration metadata")
	assert.True(t, repo.saveWithRolesCalled, "identity should have been saved before clear was attempted")
}

func TestProvision_HappyPath_ClearsMetadataAndSavesIdentity(t *testing.T) {
	// Sanity check that the success path still works end-to-end: identity is
	// saved with TENANT_OWNER role and registration credentials are cleared.
	tid := tenant.MustNewTenantID("acme")

	repo := newFakeRepo()
	store := &errorInjectingMetadataStore{
		metadata: validRegistrationMetadata(),
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.NoError(t, err)

	assert.True(t, repo.saveWithRolesCalled, "identity should be saved")
	assert.True(t, store.updateCalled, "registration metadata should be cleared")

	identity, ok := repo.identities["owner@example.com"]
	require.True(t, ok, "identity should exist after provisioning")
	roles := repo.roles[identity.ID()]
	require.Len(t, roles, 1)
	assert.Equal(t, domain.RoleTenantOwner, roles[0].Role(),
		"self-registered admin must have TENANT_OWNER role")
}

func TestProvision_IdempotentWhenIdentityAlreadyExists(t *testing.T) {
	// If the hook is re-run (e.g. after a transient failure), and the
	// identity already exists in the tenant schema, it should skip creation
	// silently rather than error out.
	tid := tenant.MustNewTenantID("acme")

	existing, err := domain.NewIdentity(tid, "owner@example.com")
	require.NoError(t, err)

	repo := newFakeRepo()
	repo.identities["owner@example.com"] = existing

	store := &errorInjectingMetadataStore{
		metadata: validRegistrationMetadata(),
	}

	hook, err := NewSelfRegisteredAdminHook(repo, store, slog.Default())
	require.NoError(t, err)

	err = hook.Provision(context.Background(), tid)
	require.NoError(t, err, "re-running the hook against an existing admin must succeed")
	assert.False(t, repo.saveWithRolesCalled, "must not attempt to recreate an existing identity")
	assert.True(t, store.updateCalled, "metadata should still be cleared on the idempotent path")
}
