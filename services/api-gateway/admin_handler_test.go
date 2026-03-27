package gateway_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	gwauth "github.com/meridianhub/meridian/services/api-gateway/auth"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adminTestRepo is a configurable stub for the admin handler tests.
// Named separately from stubIdentityRepo to avoid duplicate type in package gateway_test.
type adminTestRepo struct {
	findByIDFn func(ctx context.Context, id uuid.UUID) (*identitydomain.Identity, error)
	saveFn     func(ctx context.Context, identity *identitydomain.Identity) error
}

func (r *adminTestRepo) Save(ctx context.Context, identity *identitydomain.Identity) error {
	if r.saveFn != nil {
		return r.saveFn(ctx, identity)
	}
	return nil
}

func (r *adminTestRepo) FindByID(ctx context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
	if r.findByIDFn != nil {
		return r.findByIDFn(ctx, id)
	}
	return nil, identitydomain.ErrIdentityNotFound
}

func (r *adminTestRepo) FindByEmail(_ context.Context, _ string) (*identitydomain.Identity, error) {
	return nil, identitydomain.ErrIdentityNotFound
}

func (r *adminTestRepo) ListByTenant(_ context.Context) ([]*identitydomain.Identity, error) {
	return nil, nil
}

func (r *adminTestRepo) SaveRoleAssignment(_ context.Context, _ *identitydomain.RoleAssignment) error {
	return nil
}

func (r *adminTestRepo) FindRoleAssignments(_ context.Context, _ uuid.UUID) ([]*identitydomain.RoleAssignment, error) {
	return nil, nil
}

func (r *adminTestRepo) SaveIdentityWithInvitation(_ context.Context, _ *identitydomain.Identity, _ *identitydomain.Invitation) error {
	return nil
}

func (r *adminTestRepo) SaveIdentityWithRoles(_ context.Context, _ *identitydomain.Identity, _ []*identitydomain.RoleAssignment) error {
	return nil
}

func (r *adminTestRepo) SaveRoleAssignments(_ context.Context, _ []*identitydomain.RoleAssignment) error {
	return nil
}

func (r *adminTestRepo) SaveInvitation(_ context.Context, _ *identitydomain.Invitation) error {
	return nil
}

func (r *adminTestRepo) FindInvitationByTokenHash(_ context.Context, _ string) (*identitydomain.Invitation, error) {
	return nil, identitydomain.ErrInvitationNotFound
}

// helpers

func newAdminHandler(t *testing.T, repo identitydomain.Repository) *gateway.AdminHandler {
	t.Helper()
	h, err := gateway.NewAdminHandler(repo, slog.Default())
	require.NoError(t, err)
	return h
}

func adminTenantID(t *testing.T) tenant.TenantID {
	t.Helper()
	tid, err := tenant.NewTenantID("test_tenant")
	require.NoError(t, err)
	return tid
}

// withAdminClaims returns a copy of r with JWT claims in context.
func withAdminClaims(r *http.Request, role, userID string) *http.Request {
	claims := &platformauth.Claims{
		UserID:   userID,
		TenantID: "test_tenant",
		Roles:    []string{role},
	}
	ctx := context.WithValue(r.Context(), gwauth.ClaimsContextKey, claims)
	tid, _ := tenant.NewTenantID("test_tenant")
	ctx = tenant.WithTenant(ctx, tid)
	return r.WithContext(ctx)
}

func postVerifyOverride(handler *gateway.AdminHandler, identityID uuid.UUID, r *http.Request) *httptest.ResponseRecorder {
	r.SetPathValue("identity_id", identityID.String())
	w := httptest.NewRecorder()
	handler.HandleVerifyOverride(w, r)
	return w
}

func makeTestIdentity(t *testing.T, status identitydomain.IdentityStatus) (*identitydomain.Identity, uuid.UUID) {
	t.Helper()
	tid := adminTenantID(t)
	id := uuid.New()
	now := time.Now()
	identity := identitydomain.ReconstructIdentity(
		id, tid, "user@example.com",
		status,
		"", "", "", 0,
		now, now, 1,
	)
	return identity, id
}

// --- Tests ---

func TestAdminHandler_HandleVerifyOverride_PendingVerification(t *testing.T) {
	identity, identityID := makeTestIdentity(t, identitydomain.IdentityStatusPendingVerification)

	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleTenantOwner.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, identitydomain.IdentityStatusActive, identity.Status())
}

func TestAdminHandler_HandleVerifyOverride_PendingInvite(t *testing.T) {
	identity, identityID := makeTestIdentity(t, identitydomain.IdentityStatusPendingInvite)

	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RolePlatformAdmin.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, identitydomain.IdentityStatusActive, identity.Status())
}

func TestAdminHandler_HandleVerifyOverride_AlreadyActive(t *testing.T) {
	identity, identityID := makeTestIdentity(t, identitydomain.IdentityStatusActive)

	var saveCount int
	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
		saveFn: func(_ context.Context, _ *identitydomain.Identity) error {
			saveCount++
			return nil
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleSuperAdmin.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, saveCount, "no save should occur for already-active identity")
}

func TestAdminHandler_HandleVerifyOverride_Locked(t *testing.T) {
	identity, identityID := makeTestIdentity(t, identitydomain.IdentityStatusLocked)

	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleTenantOwner.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestAdminHandler_HandleVerifyOverride_NonAdmin_NoClaims(t *testing.T) {
	identityID := uuid.New()
	repo := &adminTestRepo{}
	handler := newAdminHandler(t, repo)

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.SetPathValue("identity_id", identityID.String())
	w := httptest.NewRecorder()
	handler.HandleVerifyOverride(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminHandler_HandleVerifyOverride_NonAdmin_RegularRole(t *testing.T) {
	identityID := uuid.New()
	repo := &adminTestRepo{}
	handler := newAdminHandler(t, repo)

	claims := &platformauth.Claims{
		UserID:   "regular-user",
		TenantID: "test_tenant",
		Roles:    []string{"operator"},
	}
	ctx := context.WithValue(context.Background(), gwauth.ClaimsContextKey, claims)
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	r.SetPathValue("identity_id", identityID.String())
	w := httptest.NewRecorder()
	handler.HandleVerifyOverride(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAdminHandler_HandleVerifyOverride_NotFound(t *testing.T) {
	identityID := uuid.New()
	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (*identitydomain.Identity, error) {
			return nil, identitydomain.ErrIdentityNotFound
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleTenantOwner.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminHandler_HandleVerifyOverride_InvalidIdentityID(t *testing.T) {
	repo := &adminTestRepo{}
	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleTenantOwner.String(),
		"admin-user-id",
	)
	r.SetPathValue("identity_id", "not-a-uuid")
	w := httptest.NewRecorder()
	handler.HandleVerifyOverride(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAdminHandler_HandleVerifyOverride_SaveError(t *testing.T) {
	identity, identityID := makeTestIdentity(t, identitydomain.IdentityStatusPendingVerification)

	repo := &adminTestRepo{
		findByIDFn: func(_ context.Context, id uuid.UUID) (*identitydomain.Identity, error) {
			if id == identityID {
				return identity, nil
			}
			return nil, identitydomain.ErrIdentityNotFound
		},
		saveFn: func(_ context.Context, _ *identitydomain.Identity) error {
			return errors.New("db error")
		},
	}

	handler := newAdminHandler(t, repo)
	r := withAdminClaims(
		httptest.NewRequest(http.MethodPost, "/", nil),
		platformauth.RoleTenantOwner.String(),
		"admin-user-id",
	)
	w := postVerifyOverride(handler, identityID, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNewAdminHandler_Validation(t *testing.T) {
	repo := &adminTestRepo{}

	_, err := gateway.NewAdminHandler(nil, slog.Default())
	assert.ErrorIs(t, err, gateway.ErrAdminIdentityRequired)

	_, err = gateway.NewAdminHandler(repo, nil)
	assert.ErrorIs(t, err, gateway.ErrAdminLoggerRequired)
}
