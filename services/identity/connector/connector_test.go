package connector_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var connTestTID = tenant.MustNewTenantID("test_tenant")

func now() time.Time { return time.Now() }

// --- Mock repository ---

type mockRepo struct {
	identity    *domain.Identity
	assignments []*domain.RoleAssignment

	findByEmailErr error
	findRoleErr    error
	saveErr        error
}

func (m *mockRepo) Save(_ context.Context, id *domain.Identity) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.identity = id
	return nil
}

func (m *mockRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.Identity, error) {
	return nil, errors.New("not implemented")
}

func (m *mockRepo) FindByEmail(_ context.Context, _ string) (*domain.Identity, error) {
	if m.findByEmailErr != nil {
		return nil, m.findByEmailErr
	}
	if m.identity == nil {
		return nil, domain.ErrIdentityNotFound
	}
	return m.identity, nil
}

func (m *mockRepo) ListByTenant(_ context.Context) ([]*domain.Identity, error) {
	return nil, errors.New("not implemented")
}

func (m *mockRepo) SaveRoleAssignment(_ context.Context, _ *domain.RoleAssignment) error {
	return errors.New("not implemented")
}

func (m *mockRepo) FindRoleAssignments(_ context.Context, _ uuid.UUID) ([]*domain.RoleAssignment, error) {
	if m.findRoleErr != nil {
		return nil, m.findRoleErr
	}
	return m.assignments, nil
}

func (m *mockRepo) SaveIdentityWithInvitation(_ context.Context, _ *domain.Identity, _ *domain.Invitation) error {
	return errors.New("not implemented")
}

func (m *mockRepo) SaveIdentityWithRoles(_ context.Context, _ *domain.Identity, _ []*domain.RoleAssignment) error {
	return errors.New("not implemented")
}

func (m *mockRepo) SaveRoleAssignments(_ context.Context, _ []*domain.RoleAssignment) error {
	return errors.New("not implemented")
}

func (m *mockRepo) SaveInvitation(_ context.Context, _ *domain.Invitation) error {
	return errors.New("not implemented")
}

func (m *mockRepo) FindInvitationByTokenHash(_ context.Context, _ string) (*domain.Invitation, error) {
	return nil, errors.New("not implemented")
}

func (m *mockRepo) SaveVerificationToken(_ context.Context, _ *domain.VerificationToken) error {
	return nil
}

func (m *mockRepo) FindVerificationTokenByHash(_ context.Context, _ string) (*domain.VerificationToken, error) {
	return nil, domain.ErrVerificationTokenNotFound
}

func (m *mockRepo) CountVerificationTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (m *mockRepo) SavePasswordResetToken(_ context.Context, _ *domain.PasswordResetToken) error {
	return nil
}

func (m *mockRepo) FindPasswordResetTokenByHash(_ context.Context, _ string) (*domain.PasswordResetToken, error) {
	return nil, domain.ErrPasswordResetTokenNotFound
}

func (m *mockRepo) CountPasswordResetTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (m *mockRepo) InvalidatePasswordResetTokensForIdentity(_ context.Context, _ uuid.UUID) error {
	return nil
}

// --- Helpers ---

const testPassword = "ValidPassword1!"

func makeActiveIdentity(t *testing.T, email string) *domain.Identity {
	t.Helper()
	id, err := domain.NewIdentity(connTestTID, email)
	require.NoError(t, err)

	hash, err := credentials.HashPassword(testPassword)
	require.NoError(t, err)
	require.NoError(t, id.SetPassword(hash))
	require.NoError(t, id.Activate())
	return id
}

func ctxWithTenant(t *testing.T, tid string) context.Context {
	t.Helper()
	tenantID, err := tenant.NewTenantID(tid)
	require.NoError(t, err)
	return tenant.WithTenant(context.Background(), tenantID)
}

func newConnector(t *testing.T, repo domain.Repository) *connector.Connector {
	t.Helper()
	c, err := connector.New(repo, slog.Default())
	require.NoError(t, err)
	return c
}

// --- New tests ---

func TestNew_NilRepository(t *testing.T) {
	_, err := connector.New(nil, nil)
	require.Error(t, err)
}

func TestNew_NilLogger_UsesDefault(t *testing.T) {
	repo := &mockRepo{identity: makeActiveIdentity(t, "a@example.com")}
	c, err := connector.New(repo, nil)
	require.NoError(t, err)
	assert.NotNil(t, c)
}

// --- Login: success ---

func TestLogin_Success(t *testing.T) {
	identity := makeActiveIdentity(t, "alice@example.com")
	repo := &mockRepo{identity: identity}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "alice@example.com", testPassword)

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, identity.ID().String(), got.UserID)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.True(t, got.EmailVerified)
}

func TestLogin_Success_PopulatesGroups(t *testing.T) {
	identity := makeActiveIdentity(t, "bob@example.com")

	// Build two active assignments.
	adminAssign := domain.ReconstructRoleAssignment(
		uuid.New(), connTestTID, identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)
	operatorAssign := domain.ReconstructRoleAssignment(
		uuid.New(), connTestTID, identity.ID(), uuid.New(),
		domain.RoleOperator, nil, nil, nil,
		now(), now(),
	)

	repo := &mockRepo{
		identity:    identity,
		assignments: []*domain.RoleAssignment{adminAssign, operatorAssign},
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "bob@example.com", testPassword)

	require.NoError(t, err)
	require.True(t, valid)
	assert.ElementsMatch(t, []string{"ADMIN", "OPERATOR"}, got.Groups)
}

func TestLogin_Success_SkipsRevokedAssignments(t *testing.T) {
	identity := makeActiveIdentity(t, "carol@example.com")

	active := domain.ReconstructRoleAssignment(
		uuid.New(), connTestTID, identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)
	// Revoked assignment — should be excluded.
	revokedBy := uuid.New()
	revokedAt := now()
	revoked := domain.ReconstructRoleAssignment(
		uuid.New(), connTestTID, identity.ID(), uuid.New(),
		domain.RoleOperator, nil, &revokedAt, &revokedBy,
		now(), now(),
	)

	repo := &mockRepo{
		identity:    identity,
		assignments: []*domain.RoleAssignment{active, revoked},
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "carol@example.com", testPassword)

	require.NoError(t, err)
	require.True(t, valid)
	assert.Equal(t, []string{"ADMIN"}, got.Groups)
}

// --- Login: wrong password ---

func TestLogin_WrongPassword_ReturnsFalse(t *testing.T) {
	identity := makeActiveIdentity(t, "dave@example.com")
	repo := &mockRepo{identity: identity}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "dave@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
	assert.Empty(t, got.UserID)
}

// --- Login: account states ---

func TestLogin_LockedAccount_ReturnsFalse(t *testing.T) {
	id, err := domain.NewIdentity(connTestTID, "locked@example.com")
	require.NoError(t, err)
	hash, err := credentials.HashPassword(testPassword)
	require.NoError(t, err)
	require.NoError(t, id.SetPassword(hash))
	require.NoError(t, id.Activate())
	// Lock via RecordLoginAttempt failures (5 attempts).
	for range 5 {
		_ = id.RecordLoginAttempt(false)
	}
	require.True(t, id.IsLocked())

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "locked@example.com", testPassword)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestLogin_SuspendedAccount_ReturnsFalse(t *testing.T) {
	id, err := domain.NewIdentity(connTestTID, "suspended@example.com")
	require.NoError(t, err)
	hash, err := credentials.HashPassword(testPassword)
	require.NoError(t, err)
	require.NoError(t, id.SetPassword(hash))
	require.NoError(t, id.Activate())
	require.NoError(t, id.Suspend())

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "suspended@example.com", testPassword)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestLogin_PendingInviteAccount_ReturnsFalse(t *testing.T) {
	// NewIdentity starts in PENDING_INVITE — no need to transition.
	id, err := domain.NewIdentity(connTestTID, "pending@example.com")
	require.NoError(t, err)

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "pending@example.com", testPassword)

	require.NoError(t, err)
	assert.False(t, valid)
}

// --- Login: not found ---

func TestLogin_IdentityNotFound_ReturnsFalse(t *testing.T) {
	repo := &mockRepo{} // identity is nil → FindByEmail returns ErrIdentityNotFound
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "nobody@example.com", testPassword)

	require.NoError(t, err)
	assert.False(t, valid)
}

// --- Login: infrastructure errors ---

func TestLogin_RepositoryError_ReturnsError(t *testing.T) {
	repo := &mockRepo{findByEmailErr: errors.New("db connection lost")}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "err@example.com", testPassword)

	require.Error(t, err)
	assert.False(t, valid)
}

func TestLogin_RoleQueryError_SucceedsWithEmptyGroups(t *testing.T) {
	// Role resolution failure is non-fatal — login should still succeed.
	identity := makeActiveIdentity(t, "grace@example.com")
	repo := &mockRepo{
		identity:    identity,
		findRoleErr: errors.New("role service unavailable"),
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "grace@example.com", testPassword)

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Empty(t, got.Groups)
}

// --- Login: missing tenant context ---

func TestLogin_MissingTenantContext_ReturnsError(t *testing.T) {
	identity := makeActiveIdentity(t, "henry@example.com")
	repo := &mockRepo{identity: identity}
	c := newConnector(t, repo)
	ctx := context.Background() // no tenant set

	_, valid, err := c.Login(ctx, nil, "henry@example.com", testPassword)

	require.Error(t, err)
	assert.False(t, valid)
}

// --- Login: save error during failed attempt ---

func TestLogin_SaveErrorOnFailedAttempt_StillReturnsFalse(t *testing.T) {
	identity := makeActiveIdentity(t, "saveerr@example.com")
	repo := &mockRepo{
		identity: identity,
		saveErr:  errors.New("db write error"),
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "saveerr@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestLogin_SaveErrorOnSuccessfulLogin_StillReturnsTrue(t *testing.T) {
	identity := makeActiveIdentity(t, "saveerr2@example.com")
	repo := &mockRepo{
		identity: identity,
		saveErr:  errors.New("db write error"),
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Login(ctx, nil, "saveerr2@example.com", testPassword)

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, identity.ID().String(), got.UserID)
}

// --- Resolve tests ---

func TestResolve_Success(t *testing.T) {
	identity := makeActiveIdentity(t, "resolve@example.com")

	adminAssign := domain.ReconstructRoleAssignment(
		uuid.New(), connTestTID, identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)

	repo := &mockRepo{
		identity:    identity,
		assignments: []*domain.RoleAssignment{adminAssign},
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	got, valid, err := c.Resolve(ctx, "resolve@example.com")

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, identity.ID().String(), got.UserID)
	assert.Equal(t, "resolve@example.com", got.Email)
	assert.True(t, got.EmailVerified)
	assert.Equal(t, []string{"ADMIN"}, got.Groups)
}

func TestResolve_IdentityNotFound_ReturnsFalse(t *testing.T) {
	repo := &mockRepo{} // identity is nil
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "nobody@example.com")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestResolve_RepositoryError_ReturnsError(t *testing.T) {
	repo := &mockRepo{findByEmailErr: errors.New("db connection lost")}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "err@example.com")

	require.Error(t, err)
	assert.False(t, valid)
}

func TestResolve_NonActiveAccount_ReturnsFalse(t *testing.T) {
	// NewIdentity starts in PENDING_INVITE — not active
	id, err := domain.NewIdentity(connTestTID, "pending-resolve@example.com")
	require.NoError(t, err)

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "pending-resolve@example.com")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestResolve_SuspendedAccount_ReturnsFalse(t *testing.T) {
	id := makeActiveIdentity(t, "suspended-resolve@example.com")
	require.NoError(t, id.Suspend())

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "suspended-resolve@example.com")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestResolve_LockedAccount_ReturnsFalse(t *testing.T) {
	id := makeActiveIdentity(t, "locked-resolve@example.com")
	// Lock via 5 failed attempts
	for range 5 {
		_ = id.RecordLoginAttempt(false)
	}
	require.True(t, id.IsLocked())

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "locked-resolve@example.com")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestResolve_RoleQueryError_ReturnsError(t *testing.T) {
	// Unlike Login (where role errors are non-fatal), Resolve treats role
	// errors as fatal because missing roles could grant incorrect permissions.
	identity := makeActiveIdentity(t, "role-err-resolve@example.com")
	repo := &mockRepo{
		identity:    identity,
		findRoleErr: errors.New("role service unavailable"),
	}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Resolve(ctx, "role-err-resolve@example.com")

	require.Error(t, err)
	assert.False(t, valid)
}

func TestResolve_MissingTenantContext_ReturnsError(t *testing.T) {
	identity := makeActiveIdentity(t, "notenant-resolve@example.com")
	repo := &mockRepo{identity: identity}
	c := newConnector(t, repo)
	ctx := context.Background() // no tenant

	_, valid, err := c.Resolve(ctx, "notenant-resolve@example.com")

	require.Error(t, err)
	assert.False(t, valid)
}

// --- Tenant propagation ---

func TestLogin_TenantContextPropagation(t *testing.T) {
	// Verifies the connector routes within tenant scope by checking it rejects
	// calls with no tenant while accepting calls with a valid tenant.
	identity := makeActiveIdentity(t, "iris@example.com")
	repo := &mockRepo{identity: identity}
	c := newConnector(t, repo)

	t.Run("with tenant context succeeds", func(t *testing.T) {
		ctx := ctxWithTenant(t, "acme_corp")
		_, valid, err := c.Login(ctx, nil, "iris@example.com", testPassword)
		require.NoError(t, err)
		assert.True(t, valid)
	})

	t.Run("without tenant context returns error", func(t *testing.T) {
		_, _, err := c.Login(context.Background(), nil, "iris@example.com", testPassword)
		require.Error(t, err)
	})
}

func TestLogin_PendingVerificationAccount_ReturnsErrEmailNotVerified(t *testing.T) {
	id, err := domain.NewSelfRegisteredIdentity(connTestTID, "unverified@example.com", true)
	require.NoError(t, err)

	repo := &mockRepo{identity: id}
	c := newConnector(t, repo)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "unverified@example.com", testPassword)

	assert.False(t, valid)
	assert.ErrorIs(t, err, domain.ErrEmailNotVerified)
}
