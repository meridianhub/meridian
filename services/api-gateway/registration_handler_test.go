package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Stub implementations ---

type stubTenantCreator struct {
	createFn func(ctx context.Context, tenantID, slug, displayName string) (string, error)
	deleteFn func(ctx context.Context, tenantID string) error
}

func (s *stubTenantCreator) CreateTenant(ctx context.Context, tenantID, slug, displayName string) (string, error) {
	return s.createFn(ctx, tenantID, slug, displayName)
}

func (s *stubTenantCreator) DeleteTenant(ctx context.Context, tenantID string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, tenantID)
	}
	return nil
}

type stubIdentityRepo struct {
	saveIdentityWithRolesFn func(ctx context.Context, identity *identitydomain.Identity, roles []*identitydomain.RoleAssignment) error
}

func (s *stubIdentityRepo) Save(_ context.Context, _ *identitydomain.Identity) error {
	return nil
}

func (s *stubIdentityRepo) FindByID(_ context.Context, _ uuid.UUID) (*identitydomain.Identity, error) {
	return nil, identitydomain.ErrIdentityNotFound
}

func (s *stubIdentityRepo) FindByEmail(_ context.Context, _ string) (*identitydomain.Identity, error) {
	return nil, identitydomain.ErrIdentityNotFound
}

func (s *stubIdentityRepo) ListByTenant(_ context.Context) ([]*identitydomain.Identity, error) {
	return nil, nil
}

func (s *stubIdentityRepo) SaveRoleAssignment(_ context.Context, _ *identitydomain.RoleAssignment) error {
	return nil
}

func (s *stubIdentityRepo) FindRoleAssignments(_ context.Context, _ uuid.UUID) ([]*identitydomain.RoleAssignment, error) {
	return nil, nil
}

func (s *stubIdentityRepo) SaveIdentityWithInvitation(_ context.Context, _ *identitydomain.Identity, _ *identitydomain.Invitation) error {
	return nil
}

func (s *stubIdentityRepo) SaveIdentityWithRoles(ctx context.Context, identity *identitydomain.Identity, roles []*identitydomain.RoleAssignment) error {
	if s.saveIdentityWithRolesFn != nil {
		return s.saveIdentityWithRolesFn(ctx, identity, roles)
	}
	return nil
}

func (s *stubIdentityRepo) SaveRoleAssignments(_ context.Context, _ []*identitydomain.RoleAssignment) error {
	return nil
}

func (s *stubIdentityRepo) SaveInvitation(_ context.Context, _ *identitydomain.Invitation) error {
	return nil
}

func (s *stubIdentityRepo) FindInvitationByTokenHash(_ context.Context, _ string) (*identitydomain.Invitation, error) {
	return nil, identitydomain.ErrInvitationNotFound
}

func (s *stubIdentityRepo) SaveVerificationToken(_ context.Context, _ *identitydomain.VerificationToken) error {
	return nil
}

func (s *stubIdentityRepo) FindVerificationTokenByHash(_ context.Context, _ string) (*identitydomain.VerificationToken, error) {
	return nil, identitydomain.ErrVerificationTokenNotFound
}

func (s *stubIdentityRepo) CountVerificationTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (s *stubIdentityRepo) SavePasswordResetToken(_ context.Context, _ *identitydomain.PasswordResetToken) error {
	return nil
}

func (s *stubIdentityRepo) FindPasswordResetTokenByHash(_ context.Context, _ string) (*identitydomain.PasswordResetToken, error) {
	return nil, identitydomain.ErrPasswordResetTokenNotFound
}

func (s *stubIdentityRepo) CountPasswordResetTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, nil
}

func (s *stubIdentityRepo) MarkPasswordResetTokensConsumedForIdentity(_ context.Context, _ uuid.UUID) error {
	return nil
}

type stubSlugChecker struct {
	isAvailableFn func(ctx context.Context, slug string) (bool, error)
}

func (s *stubSlugChecker) IsSlugAvailable(ctx context.Context, slug string) (bool, error) {
	return s.isAvailableFn(ctx, slug)
}

// --- Helper ---

func defaultStubs() (*stubTenantCreator, *stubIdentityRepo) {
	tc := &stubTenantCreator{
		createFn: func(_ context.Context, tenantID, _, _ string) (string, error) {
			return tenantID, nil
		},
	}
	ir := &stubIdentityRepo{}
	return tc, ir
}

func newRegistrationHandler(t *testing.T, tc gateway.TenantCreator, ir identitydomain.Repository) *gateway.RegistrationHandler {
	t.Helper()
	h, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		IdentityRepo:  ir,
		RateLimiter:   gateway.NewRegistrationRateLimiter(100), // high limit for tests
		BaseDomain:    "meridian.app",
		Logger:        slog.Default(),
	})
	require.NoError(t, err)
	return h
}

func newRegistrationHandlerWithSlugChecker(t *testing.T, tc gateway.TenantCreator, ir identitydomain.Repository, sc gateway.SlugChecker) *gateway.RegistrationHandler {
	t.Helper()
	h, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		IdentityRepo:  ir,
		SlugChecker:   sc,
		RateLimiter:   gateway.NewRegistrationRateLimiter(100),
		BaseDomain:    "meridian.app",
		Logger:        slog.Default(),
	})
	require.NoError(t, err)
	return h
}

func postRegister(handler *gateway.RegistrationHandler, body map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/register", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleRegister(w, r)
	return w
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// --- Tests ---

func TestRegistrationHandler_Success(t *testing.T) {
	tc, ir := defaultStubs()

	var capturedTenantID, capturedSlug, capturedDisplayName string
	tc.createFn = func(_ context.Context, tenantID, slug, displayName string) (string, error) {
		capturedTenantID = tenantID
		capturedSlug = slug
		capturedDisplayName = displayName
		return tenantID, nil
	}

	var capturedIdentityEmail string
	var capturedRoleCount int
	ir.saveIdentityWithRolesFn = func(_ context.Context, identity *identitydomain.Identity, roles []*identitydomain.RoleAssignment) error {
		capturedIdentityEmail = identity.Email()
		capturedRoleCount = len(roles)
		return nil
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":         "acme-corp",
		"email":        "admin@acme.com",
		"password":     "SecurePass123!",
		"display_name": "Acme Corporation",
	})

	assert.Equal(t, http.StatusCreated, w.Code)

	resp := parseResponse(t, w)
	assert.Equal(t, "acme_corp", resp["tenant_id"])
	assert.Equal(t, "https://acme-corp.meridian.app/login", resp["login_url"])

	// Verify tenant creation was called correctly.
	assert.Equal(t, "acme_corp", capturedTenantID)
	assert.Equal(t, "acme-corp", capturedSlug)
	assert.Equal(t, "Acme Corporation", capturedDisplayName)

	// Verify identity was created with TENANT_OWNER role.
	assert.Equal(t, "admin@acme.com", capturedIdentityEmail)
	assert.Equal(t, 1, capturedRoleCount)
}

func TestRegistrationHandler_DefaultDisplayName(t *testing.T) {
	tc, ir := defaultStubs()

	var capturedDisplayName string
	tc.createFn = func(_ context.Context, tenantID, _, displayName string) (string, error) {
		capturedDisplayName = displayName
		return tenantID, nil
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "acme-corp", capturedDisplayName)
}

func TestRegistrationHandler_MissingRequiredFields(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)

	tests := []struct {
		name string
		body map[string]string
	}{
		{"missing slug", map[string]string{"email": "a@b.com", "password": "SecurePass123!"}},
		{"missing email", map[string]string{"slug": "acme-corp", "password": "SecurePass123!"}},
		{"missing password", map[string]string{"slug": "acme-corp", "email": "a@b.com"}},
		{"empty body", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := postRegister(h, tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			resp := parseResponse(t, w)
			assert.Contains(t, resp["error"], "required")
		})
	}
}

func TestRegistrationHandler_InvalidSlug(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)

	tests := []struct {
		name string
		slug string
	}{
		{"too short", "ab"},
		{"uppercase", "Acme-Corp"},
		{"leading hyphen", "-acme"},
		{"trailing hyphen", "acme-"},
		{"reserved word", "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := postRegister(h, map[string]string{
				"slug":     tt.slug,
				"email":    "admin@acme.com",
				"password": "SecurePass123!",
			})
			assert.Equal(t, http.StatusBadRequest, w.Code)
			resp := parseResponse(t, w)
			assert.Contains(t, resp["error"], "slug")
		})
	}
}

func TestRegistrationHandler_WeakPassword(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)

	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "weak",
	})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "password")
}

func TestRegistrationHandler_SlugTaken(t *testing.T) {
	tc, ir := defaultStubs()
	tc.createFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "", status.Error(codes.AlreadyExists, "tenant acme_corp already exists")
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusConflict, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "taken")

	_ = ir // identity repo should not be called
}

func TestRegistrationHandler_TenantCreationFails(t *testing.T) {
	tc, ir := defaultStubs()
	tc.createFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "", errors.New("database connection lost")
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRegistrationHandler_EmailAlreadyRegistered(t *testing.T) {
	tc, ir := defaultStubs()
	ir.saveIdentityWithRolesFn = func(_ context.Context, _ *identitydomain.Identity, _ []*identitydomain.RoleAssignment) error {
		return identitydomain.ErrEmailAlreadyExists
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusConflict, w.Code)
	resp := parseResponse(t, w)
	assert.Contains(t, resp["error"], "already registered")
}

func TestRegistrationHandler_IdentitySaveFailsCompensatesTenant(t *testing.T) {
	tc, ir := defaultStubs()
	ir.saveIdentityWithRolesFn = func(_ context.Context, _ *identitydomain.Identity, _ []*identitydomain.RoleAssignment) error {
		return errors.New("DB write error")
	}

	var deletedTenantID string
	tc.deleteFn = func(_ context.Context, tenantID string) error {
		deletedTenantID = tenantID
		return nil
	}

	h := newRegistrationHandler(t, tc, ir)
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "acme_corp", deletedTenantID, "tenant should be deleted as compensation")
}

func TestRegistrationHandler_RateLimiting(t *testing.T) {
	tc, ir := defaultStubs()
	h, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		IdentityRepo:  ir,
		RateLimiter:   gateway.NewRegistrationRateLimiter(2),
		BaseDomain:    "meridian.app",
		Logger:        slog.Default(),
	})
	require.NoError(t, err)

	// First 2 requests succeed.
	for i := 0; i < 2; i++ {
		w := postRegister(h, map[string]string{
			"slug":     "acme-corp",
			"email":    "admin@acme.com",
			"password": "SecurePass123!",
		})
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d should not be rate limited", i+1)
	}

	// 3rd request is rate limited.
	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestRegistrationHandler_MethodNotAllowed(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/register", nil)
	w := httptest.NewRecorder()
	h.HandleRegister(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRegistrationHandler_InvalidJSON(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/register", bytes.NewReader([]byte("not json")))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleRegister(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegistrationHandler_NoBaseDomainFallbackLoginURL(t *testing.T) {
	tc, ir := defaultStubs()
	h, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		IdentityRepo:  ir,
		RateLimiter:   gateway.NewRegistrationRateLimiter(100),
		Logger:        slog.Default(),
		// BaseDomain intentionally empty.
	})
	require.NoError(t, err)

	w := postRegister(h, map[string]string{
		"slug":     "acme-corp",
		"email":    "admin@acme.com",
		"password": "SecurePass123!",
	})

	assert.Equal(t, http.StatusCreated, w.Code)
	resp := parseResponse(t, w)
	assert.Equal(t, "/login?tenant=acme-corp", resp["login_url"])
}

func TestNewRegistrationHandler_Validation(t *testing.T) {
	tc, ir := defaultStubs()

	_, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		Logger: slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrRegistrationTenantRequired)

	_, err = gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		Logger:        slog.Default(),
	})
	assert.ErrorIs(t, err, gateway.ErrRegistrationIdentityRequired)

	_, err = gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: tc,
		IdentityRepo:  ir,
	})
	assert.ErrorIs(t, err, gateway.ErrRegistrationLoggerRequired)
}

// --- Slug Availability Tests ---

func slugAvailableRequest(handler *gateway.RegistrationHandler, slug string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/slugs/"+slug+"/available", nil)
	r.SetPathValue("slug", slug)
	w := httptest.NewRecorder()
	handler.HandleSlugAvailable(w, r)
	return w
}

func TestSlugAvailable_Available(t *testing.T) {
	tc, ir := defaultStubs()
	sc := &stubSlugChecker{
		isAvailableFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
	}
	h := newRegistrationHandlerWithSlugChecker(t, tc, ir, sc)
	w := slugAvailableRequest(h, "my-org")

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseResponse(t, w)
	assert.Equal(t, true, resp["available"])
	assert.Nil(t, resp["reason"])
}

func TestSlugAvailable_Taken(t *testing.T) {
	tc, ir := defaultStubs()
	sc := &stubSlugChecker{
		isAvailableFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
	}
	h := newRegistrationHandlerWithSlugChecker(t, tc, ir, sc)
	w := slugAvailableRequest(h, "taken-org")

	assert.Equal(t, http.StatusOK, w.Code)
	resp := parseResponse(t, w)
	assert.Equal(t, false, resp["available"])
	assert.Equal(t, "slug is already taken", resp["reason"])
}

func TestSlugAvailable_InvalidSlug(t *testing.T) {
	tc, ir := defaultStubs()
	sc := &stubSlugChecker{
		isAvailableFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
	}
	h := newRegistrationHandlerWithSlugChecker(t, tc, ir, sc)

	tests := []struct {
		name string
		slug string
	}{
		{"too short", "ab"},
		{"uppercase", "Acme"},
		{"leading hyphen", "-org"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := slugAvailableRequest(h, tt.slug)
			assert.Equal(t, http.StatusOK, w.Code)
			resp := parseResponse(t, w)
			assert.Equal(t, false, resp["available"])
			assert.Contains(t, resp["reason"], "invalid slug")
		})
	}
}

func TestSlugAvailable_EmptySlug(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir)
	w := slugAvailableRequest(h, "")

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSlugAvailable_NoSlugChecker(t *testing.T) {
	tc, ir := defaultStubs()
	h := newRegistrationHandler(t, tc, ir) // no slug checker configured
	w := slugAvailableRequest(h, "my-org")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSlugAvailable_CheckerError(t *testing.T) {
	tc, ir := defaultStubs()
	sc := &stubSlugChecker{
		isAvailableFn: func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("database connection lost")
		},
	}
	h := newRegistrationHandlerWithSlugChecker(t, tc, ir, sc)
	w := slugAvailableRequest(h, "my-org")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestSlugAvailable_MethodNotAllowed(t *testing.T) {
	tc, ir := defaultStubs()
	sc := &stubSlugChecker{
		isAvailableFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
	}
	h := newRegistrationHandlerWithSlugChecker(t, tc, ir, sc)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/slugs/my-org/available", nil)
	r.SetPathValue("slug", "my-org")
	w := httptest.NewRecorder()
	h.HandleSlugAvailable(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
