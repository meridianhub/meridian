package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo implements domain.Repository for bootstrap unit tests.
type fakeRepo struct {
	identities map[string]*domain.Identity            // keyed by email
	roles      map[uuid.UUID][]*domain.RoleAssignment // keyed by identity ID

	saveWithRolesCalled bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		identities: make(map[string]*domain.Identity),
		roles:      make(map[uuid.UUID][]*domain.RoleAssignment),
	}
}

func (f *fakeRepo) Save(_ context.Context, identity *domain.Identity) error {
	f.identities[identity.Email()] = identity
	return nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Identity, error) {
	for _, ident := range f.identities {
		if ident.ID() == id {
			return ident, nil
		}
	}
	return nil, domain.ErrIdentityNotFound
}

func (f *fakeRepo) FindByEmail(_ context.Context, email string) (*domain.Identity, error) {
	if ident, ok := f.identities[email]; ok {
		return ident, nil
	}
	return nil, domain.ErrIdentityNotFound
}

func (f *fakeRepo) ListByTenant(_ context.Context) ([]*domain.Identity, error) {
	result := make([]*domain.Identity, 0, len(f.identities))
	for _, ident := range f.identities {
		result = append(result, ident)
	}
	return result, nil
}

func (f *fakeRepo) SaveRoleAssignment(_ context.Context, assignment *domain.RoleAssignment) error {
	f.roles[assignment.IdentityID()] = append(f.roles[assignment.IdentityID()], assignment)
	return nil
}

func (f *fakeRepo) FindRoleAssignments(_ context.Context, identityID uuid.UUID) ([]*domain.RoleAssignment, error) {
	return f.roles[identityID], nil
}

func (f *fakeRepo) SaveIdentityWithInvitation(_ context.Context, identity *domain.Identity, _ *domain.Invitation) error {
	f.identities[identity.Email()] = identity
	return nil
}

func (f *fakeRepo) SaveIdentityWithRoles(_ context.Context, identity *domain.Identity, roles []*domain.RoleAssignment) error {
	f.saveWithRolesCalled = true
	f.identities[identity.Email()] = identity
	f.roles[identity.ID()] = append(f.roles[identity.ID()], roles...)
	return nil
}

func (f *fakeRepo) SaveRoleAssignments(_ context.Context, assignments []*domain.RoleAssignment) error {
	for _, a := range assignments {
		f.roles[a.IdentityID()] = append(f.roles[a.IdentityID()], a)
	}
	return nil
}

func (f *fakeRepo) SaveInvitation(_ context.Context, _ *domain.Invitation) error {
	return nil
}

func (f *fakeRepo) FindInvitationByTokenHash(_ context.Context, _ string) (*domain.Invitation, error) {
	return nil, errors.New("not found")
}

// --- Tests for DemoUser and loadDemoUsers ---

func TestLoadDemoUsers_ReturnsConfiguredUsers(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")
	t.Setenv("DEMO_OPERATOR_TENANT", "volterra")

	users := loadDemoUsers()

	require.Len(t, users, 1)
	assert.Equal(t, "operator@volterra.energy", users[0].Email)
	assert.Equal(t, "demo2026", users[0].Password)
	assert.Equal(t, "volterra", users[0].TenantID)
	assert.Equal(t, "OPERATOR", users[0].Role)
}

func TestLoadDemoUsers_DefaultTenant(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")

	users := loadDemoUsers()

	require.Len(t, users, 1)
	assert.Equal(t, "volterra", users[0].TenantID)
}

func TestLoadDemoUsers_MissingEmail_Skipped(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")

	users := loadDemoUsers()
	assert.Empty(t, users)
}

func TestLoadDemoUsers_MissingPassword_Skipped(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")

	users := loadDemoUsers()
	assert.Empty(t, users)
}

// --- Tests for SeedDemoUsers ---

func TestSeedDemoUsers_NilRepo(t *testing.T) {
	err := SeedDemoUsers(context.Background(), nil)
	assert.ErrorIs(t, err, ErrNilRepository)
}

func TestSeedDemoUsers_NoEnvVars_NoOp(t *testing.T) {
	repo := newFakeRepo()
	err := SeedDemoUsers(context.Background(), repo)

	require.NoError(t, err)
	assert.False(t, repo.saveWithRolesCalled)
}

func TestSeedDemoUsers_CreatesNewUser(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")
	t.Setenv("DEMO_OPERATOR_TENANT", "volterra")

	repo := newFakeRepo()
	err := SeedDemoUsers(context.Background(), repo)

	require.NoError(t, err)
	assert.True(t, repo.saveWithRolesCalled)

	ident, ok := repo.identities["operator@volterra.energy"]
	require.True(t, ok, "identity should exist in repo")
	assert.Equal(t, "operator@volterra.energy", ident.Email())
	assert.Equal(t, domain.IdentityStatusActive, ident.Status())
	assert.NotEmpty(t, ident.PasswordHash())

	roles := repo.roles[ident.ID()]
	require.Len(t, roles, 1)
	assert.Equal(t, domain.RoleOperator, roles[0].Role())
}

func TestSeedDemoUsers_Idempotent_ExistingUser(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")
	t.Setenv("DEMO_OPERATOR_TENANT", "volterra")

	repo := newFakeRepo()

	err := SeedDemoUsers(context.Background(), repo)
	require.NoError(t, err)
	assert.True(t, repo.saveWithRolesCalled)

	repo.saveWithRolesCalled = false

	err = SeedDemoUsers(context.Background(), repo)
	require.NoError(t, err)
	assert.False(t, repo.saveWithRolesCalled, "should not create identity again")
}

func TestSeedDemoUsers_ReconcilesRole_ExistingUserMissingRole(t *testing.T) {
	t.Setenv("DEMO_OPERATOR_EMAIL", "operator@volterra.energy")
	t.Setenv("DEMO_OPERATOR_PASSWORD", "demo2026")
	t.Setenv("DEMO_OPERATOR_TENANT", "volterra")

	repo := newFakeRepo()

	// Pre-seed an identity with no role assignments.
	identity, err := domain.NewIdentity(tenant.MustNewTenantID("volterra"), "operator@volterra.energy")
	require.NoError(t, err)
	require.NoError(t, identity.Activate())
	repo.identities["operator@volterra.energy"] = identity

	err = SeedDemoUsers(context.Background(), repo)
	require.NoError(t, err)

	// Should NOT have called SaveIdentityWithRoles (user already exists).
	assert.False(t, repo.saveWithRolesCalled)

	// Should have reconciled the missing role via SaveRoleAssignments.
	roles := repo.roles[identity.ID()]
	require.Len(t, roles, 1)
	assert.Equal(t, domain.RoleOperator, roles[0].Role())
}
