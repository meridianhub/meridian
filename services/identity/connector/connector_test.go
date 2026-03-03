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

// --- Helpers ---

const testPassword = "ValidPassword1!"

func makeActiveIdentity(t *testing.T, email string) *domain.Identity {
	t.Helper()
	id, err := domain.NewIdentity(email)
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
		uuid.New(), identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)
	operatorAssign := domain.ReconstructRoleAssignment(
		uuid.New(), identity.ID(), uuid.New(),
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
		uuid.New(), identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)
	// Revoked assignment — should be excluded.
	revokedBy := uuid.New()
	revokedAt := now()
	revoked := domain.ReconstructRoleAssignment(
		uuid.New(), identity.ID(), uuid.New(),
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
	id, err := domain.NewIdentity("locked@example.com")
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
	id, err := domain.NewIdentity("suspended@example.com")
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
	id, err := domain.NewIdentity("pending@example.com")
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
