package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	dexconnector "github.com/dexidp/dex/connector"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/services/identity/domain"
	tenantdomain "github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock tenant resolver ---

type mockTenantResolver struct {
	tenants map[string]*tenantdomain.Tenant
	err     error
}

func (m *mockTenantResolver) GetBySlug(_ context.Context, slug string) (*tenantdomain.Tenant, error) {
	if m.err != nil {
		return nil, m.err
	}
	t, ok := m.tenants[slug]
	if !ok {
		return nil, tenantdomain.ErrNotFound
	}
	return t, nil
}

func makeTenant(t *testing.T, slug, tenantIDStr string) *tenantdomain.Tenant {
	t.Helper()
	tenantID := tenant.MustNewTenantID(tenantIDStr)
	return &tenantdomain.Tenant{
		ID:     tenantID,
		Slug:   slug,
		Status: tenantdomain.StatusActive,
	}
}

func newDexAdapter(t *testing.T, repo *mockRepo, resolver *mockTenantResolver) *connector.DexPasswordConnector {
	t.Helper()
	c := newConnector(t, repo)
	adapter, err := connector.NewDexPasswordConnector(c, resolver, slog.Default())
	require.NoError(t, err)
	return adapter
}

// --- Construction tests ---

func TestNewDexPasswordConnector_NilConnector(t *testing.T) {
	resolver := &mockTenantResolver{}
	_, err := connector.NewDexPasswordConnector(nil, resolver, nil)
	require.ErrorIs(t, err, connector.ErrConnectorNil)
}

func TestNewDexPasswordConnector_NilResolver(t *testing.T) {
	c := newConnector(t, &mockRepo{identity: makeActiveIdentity(t, "a@example.com")})
	_, err := connector.NewDexPasswordConnector(c, nil, nil)
	require.ErrorIs(t, err, connector.ErrTenantResolverNil)
}

func TestNewDexPasswordConnector_NilLogger(t *testing.T) {
	c := newConnector(t, &mockRepo{identity: makeActiveIdentity(t, "a@example.com")})
	resolver := &mockTenantResolver{}
	adapter, err := connector.NewDexPasswordConnector(c, resolver, nil)
	require.NoError(t, err)
	assert.NotNil(t, adapter)
}

// --- Prompt ---

func TestDexPasswordConnector_Prompt(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{},
	)
	assert.Equal(t, "Email", adapter.Prompt())
}

// --- Login: success ---

func TestDexLogin_Success(t *testing.T) {
	identity := makeActiveIdentity(t, "alice@example.com")
	repo := &mockRepo{identity: identity}
	resolver := &mockTenantResolver{
		tenants: map[string]*tenantdomain.Tenant{
			"volterra": makeTenant(t, "volterra", "volterra"),
		},
	}
	adapter := newDexAdapter(t, repo, resolver)

	got, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{Groups: true},
		"tenant:volterra/alice@example.com",
		testPassword,
	)

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, identity.ID().String(), got.UserID)
	assert.Equal(t, "alice@example.com", got.Email)
	assert.True(t, got.EmailVerified)
	// Should include tenant group prefix
	assert.Contains(t, got.Groups, "tenant:volterra")
	// ConnectorData should contain tenant_id
	assert.NotEmpty(t, got.ConnectorData)

	// Verify ConnectorData structure
	var cd struct {
		TenantID string `json:"tenant_id"`
	}
	require.NoError(t, json.Unmarshal(got.ConnectorData, &cd))
	assert.Equal(t, "volterra", cd.TenantID)
}

func TestDexLogin_Success_PreservesRoleGroups(t *testing.T) {
	identity := makeActiveIdentity(t, "bob@example.com")
	adminAssign := domain.ReconstructRoleAssignment(
		uuid.New(), identity.ID(), uuid.New(),
		domain.RoleAdmin, nil, nil, nil,
		now(), now(),
	)
	repo := &mockRepo{
		identity:    identity,
		assignments: []*domain.RoleAssignment{adminAssign},
	}
	resolver := &mockTenantResolver{
		tenants: map[string]*tenantdomain.Tenant{
			"acme": makeTenant(t, "acme", "acme_corp"),
		},
	}
	adapter := newDexAdapter(t, repo, resolver)

	got, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{Groups: true},
		"tenant:acme/bob@example.com",
		testPassword,
	)

	require.NoError(t, err)
	require.True(t, valid)
	// Should have both tenant group and role groups
	assert.Contains(t, got.Groups, "tenant:acme_corp")
	assert.Contains(t, got.Groups, "ADMIN")
}

// --- Login: username format ---

func TestDexLogin_MissingTenantPrefix_ReturnsFalse(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"alice@example.com",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestDexLogin_MissingSeparator_ReturnsFalse(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:volterra",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestDexLogin_EmptySlug_ReturnsFalse(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:/alice@example.com",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestDexLogin_EmptyEmail_ReturnsFalse(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:volterra/",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

// --- Login: tenant resolution ---

func TestDexLogin_TenantNotFound_ReturnsFalse(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{tenants: map[string]*tenantdomain.Tenant{}},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:unknown/alice@example.com",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestDexLogin_TenantResolverError_ReturnsError(t *testing.T) {
	adapter := newDexAdapter(t,
		&mockRepo{identity: makeActiveIdentity(t, "a@example.com")},
		&mockTenantResolver{err: errors.New("db down")},
	)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:volterra/alice@example.com",
		testPassword,
	)

	require.Error(t, err)
	assert.False(t, valid)
}

// --- Login: wrong password through adapter ---

func TestDexLogin_WrongPassword_ReturnsFalse(t *testing.T) {
	identity := makeActiveIdentity(t, "dave@example.com")
	repo := &mockRepo{identity: identity}
	resolver := &mockTenantResolver{
		tenants: map[string]*tenantdomain.Tenant{
			"volterra": makeTenant(t, "volterra", "volterra"),
		},
	}
	adapter := newDexAdapter(t, repo, resolver)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:volterra/dave@example.com",
		"WrongPassword999!",
	)

	require.NoError(t, err)
	assert.False(t, valid)
}

// --- Login: identity not found ---

func TestDexLogin_IdentityNotFound_ReturnsFalse(t *testing.T) {
	repo := &mockRepo{} // nil identity
	resolver := &mockTenantResolver{
		tenants: map[string]*tenantdomain.Tenant{
			"volterra": makeTenant(t, "volterra", "volterra"),
		},
	}
	adapter := newDexAdapter(t, repo, resolver)

	_, valid, err := adapter.Login(
		context.Background(),
		dexconnector.Scopes{},
		"tenant:volterra/nobody@example.com",
		testPassword,
	)

	require.NoError(t, err)
	assert.False(t, valid)
}
