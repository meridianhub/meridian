//go:build integration
// +build integration

package identitye2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	"github.com/meridianhub/meridian/services/identity/connector"
	identitysvc "github.com/meridianhub/meridian/services/identity/service"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const (
	tenantAlpha = "tenant_alpha"
	tenantBravo = "tenant_bravo"
)

// multiTenantInfra holds infrastructure for multi-tenant BFF auth flow tests.
type multiTenantInfra struct {
	db         *gorm.DB
	svcAlpha   *identitysvc.Service
	svcBravo   *identitysvc.Service
	ctxAlpha   context.Context
	ctxBravo   context.Context
	connAlpha  *connector.Connector
	connBravo  *connector.Connector
	signer     *platformauth.JWTSigner
	middleware *auth.JWTMiddleware
	tenantAuth *auth.TenantAuthorizationMiddleware
	logger     *slog.Logger
}

// setupMultiTenantInfra creates a test environment with two tenants,
// identity services, connectors, a JWT signer, and gateway middleware.
func setupMultiTenantInfra(t *testing.T) *multiTenantInfra {
	t.Helper()

	infra := &multiTenantInfra{}
	infra.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	infra.db = db

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Set up tenant alpha schema and apply migrations
	tcAlpha := testdb.SetupTenantSchema(t, db, tenantAlpha)
	t.Cleanup(tcAlpha.Cleanup)
	applyIdentityMigrations(t, db)

	// Set up tenant bravo schema and apply migrations
	tcBravo := testdb.SetupTenantSchema(t, db, tenantBravo)
	t.Cleanup(tcBravo.Cleanup)
	applyIdentityMigrations(t, db)

	infra.ctxAlpha = tcAlpha.Ctx
	infra.ctxBravo = tcBravo.Ctx

	// Create service and connector instances (same DB, different tenant contexts)
	repo := persistence.NewRepository(db)
	svcAlpha, err := identitysvc.NewService(repo, infra.logger)
	require.NoError(t, err)
	infra.svcAlpha = svcAlpha

	svcBravo, err := identitysvc.NewService(repo, infra.logger)
	require.NoError(t, err)
	infra.svcBravo = svcBravo

	connAlpha, err := connector.New(repo, infra.logger)
	require.NoError(t, err)
	infra.connAlpha = connAlpha

	connBravo, err := connector.New(repo, infra.logger)
	require.NoError(t, err)
	infra.connBravo = connBravo

	// Create JWT signer for BFF token issuance
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{})
	require.NoError(t, err)
	infra.signer = signer

	// Create JWT validator using the signer's public key (for BFF-signed tokens)
	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	staticValidator := &staticKeyValidator{validator: validator}

	middleware, err := auth.NewJWTMiddleware(staticValidator, infra.logger)
	require.NoError(t, err)
	infra.middleware = middleware

	infra.tenantAuth = auth.NewTenantAuthorizationMiddleware(infra.logger)

	t.Cleanup(func() { cleanup() })

	return infra
}

// inviteAndActivateUser creates a user via invite/accept flow in a tenant.
func inviteAndActivateUser(t *testing.T, svc *identitysvc.Service, ctx context.Context, email, password string) string {
	t.Helper()

	inviterID := uuid.New()
	authCtx := contextWithAuth(ctx, inviterID, []string{"TENANT_OWNER"})

	inviteResp, err := svc.InviteUser(authCtx, &pb.InviteUserRequest{Email: email})
	require.NoError(t, err)

	_, err = svc.AcceptInvitation(ctx, &pb.AcceptInvitationRequest{
		Token:    inviteResp.InvitationToken,
		Password: password,
	})
	require.NoError(t, err)

	return inviteResp.Identity.Id
}

// =============================================================================
// Test 7: Multi-Tenant BFF Login - Same Email, Different Passwords
// =============================================================================

func TestE2E_MultiTenant_BFF_PasswordLogin(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	email := "shared@example.com"
	passwordAlpha := "AlphaPass123!"
	passwordBravo := "BravoPass456!"

	// Create the same email in both tenants with different passwords
	idAlpha := inviteAndActivateUser(t, infra.svcAlpha, infra.ctxAlpha, email, passwordAlpha)
	idBravo := inviteAndActivateUser(t, infra.svcBravo, infra.ctxBravo, email, passwordBravo)

	// Verify different identity IDs (tenant isolation at identity layer)
	assert.NotEqual(t, idAlpha, idBravo, "same email in different tenants should produce different identities")

	t.Run("alpha tenant login with correct password succeeds", func(t *testing.T) {
		identity, valid, err := infra.connAlpha.Login(infra.ctxAlpha, nil, email, passwordAlpha)
		require.NoError(t, err)
		assert.True(t, valid)
		assert.Equal(t, idAlpha, identity.UserID)
	})

	t.Run("bravo tenant login with correct password succeeds", func(t *testing.T) {
		identity, valid, err := infra.connBravo.Login(infra.ctxBravo, nil, email, passwordBravo)
		require.NoError(t, err)
		assert.True(t, valid)
		assert.Equal(t, idBravo, identity.UserID)
	})

	t.Run("alpha tenant login with bravo password fails", func(t *testing.T) {
		_, valid, err := infra.connAlpha.Login(infra.ctxAlpha, nil, email, passwordBravo)
		require.NoError(t, err)
		assert.False(t, valid, "wrong password should fail even though valid in another tenant")
	})

	t.Run("bravo tenant login with alpha password fails", func(t *testing.T) {
		_, valid, err := infra.connBravo.Login(infra.ctxBravo, nil, email, passwordAlpha)
		require.NoError(t, err)
		assert.False(t, valid, "wrong password should fail even though valid in another tenant")
	})
}

// =============================================================================
// Test 8: BFF Login → JWT → Gateway Middleware (Full BFF Pipeline)
// =============================================================================

func TestE2E_BFF_LoginAndJWTValidation(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	email := "bff-user@example.com"
	password := "BffTestPass123!"

	// Set up user in alpha tenant with OPERATOR role
	idAlpha := inviteAndActivateUser(t, infra.svcAlpha, infra.ctxAlpha, email, password)

	ownerID := uuid.New()
	ownerCtx := contextWithAuth(infra.ctxAlpha, ownerID, []string{"TENANT_OWNER"})
	_, err := infra.svcAlpha.GrantRole(ownerCtx, &pb.GrantRoleRequest{
		IdentityId: idAlpha,
		Role:       pb.Role_ROLE_OPERATOR,
	})
	require.NoError(t, err)

	// Step 1: BFF login - connector validates credentials and returns identity
	identity, valid, err := infra.connAlpha.Login(infra.ctxAlpha, nil, email, password)
	require.NoError(t, err)
	require.True(t, valid)

	// Step 2: BFF builds claims and signs JWT (mirrors auth_handler.go logic)
	claims := connector.BuildClaims(identity, tenant.TenantID(tenantAlpha))
	tokenStr, err := infra.signer.SignClaims(claims, time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)

	// Step 3: Send JWT through gateway middleware
	var capturedCtx context.Context
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
	})

	handler := infra.middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Step 4: Verify gateway accepted the BFF-signed token
	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedCtx)

	userID, ok := auth.GetUserIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, idAlpha, userID)

	tenantID, ok := auth.GetTenantIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, tenantAlpha, tenantID)

	roles, ok := auth.GetRolesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Contains(t, roles, "OPERATOR")
}

// =============================================================================
// Test 9: Cross-Tenant Token Rejection via Full Pipeline
// =============================================================================

func TestE2E_BFF_CrossTenantTokenRejection(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	email := "cross-tenant@example.com"
	password := "CrossTestPass123!"

	// Create user in alpha tenant
	inviteAndActivateUser(t, infra.svcAlpha, infra.ctxAlpha, email, password)

	// Login as alpha tenant user
	identity, valid, err := infra.connAlpha.Login(infra.ctxAlpha, nil, email, password)
	require.NoError(t, err)
	require.True(t, valid)

	// Sign JWT with alpha tenant claims
	claims := connector.BuildClaims(identity, tenant.TenantID(tenantAlpha))
	tokenStr, err := infra.signer.SignClaims(claims, time.Hour)
	require.NoError(t, err)

	// Build full pipeline: JWT middleware -> tenant inject (bravo) -> tenant auth -> handler
	var nextCalled bool
	finalHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		nextCalled = true
	})

	tenantAuthHandler := infra.tenantAuth.Handler(finalHandler)

	// Simulate subdomain resolving to bravo tenant (mismatch with alpha JWT)
	tenantInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid, err := tenant.NewTenantID(tenantBravo)
		require.NoError(t, err)
		ctx := tenant.WithTenant(r.Context(), tid)
		tenantAuthHandler.ServeHTTP(w, r.WithContext(ctx))
	})

	handler := infra.middleware.Handler(tenantInjector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.False(t, nextCalled, "downstream handler must not be called on tenant mismatch")

	var body errorBody
	err = json.NewDecoder(rr.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body.Error, "not authorized for this tenant")
}

// =============================================================================
// Test 10: Groups-to-Roles Mapping via Gateway Middleware
// =============================================================================

func TestE2E_GroupsToRolesFallback(t *testing.T) {
	infra := setupMultiTenantInfra(t)

	// For these tests we sign JWTs with the signer (MapClaims), which lets us
	// control exactly which JSON keys appear in the token.

	t.Run("groups used as effective roles when roles claim absent", func(t *testing.T) {
		// Build claims map with groups but no roles key
		claimsMap := map[string]interface{}{
			"sub":         uuid.New().String(),
			"x-tenant-id": tenantAlpha,
			"email":       "sso-groups@example.com",
			"groups":      []string{"platform-admin", "OPERATOR"},
		}
		tokenStr, err := infra.signer.SignClaims(claimsMap, time.Hour)
		require.NoError(t, err)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := infra.middleware.Handler(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedCtx)

		// GetRoles calls EffectiveRoles which falls back to Groups
		roles, ok := auth.GetRolesFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Contains(t, roles, "platform-admin")
		assert.Contains(t, roles, "OPERATOR")
	})

	t.Run("roles take precedence over groups", func(t *testing.T) {
		claimsMap := map[string]interface{}{
			"sub":         uuid.New().String(),
			"x-tenant-id": tenantAlpha,
			"email":       "roles-precedence@example.com",
			"roles":       []string{"ADMIN"},
			"groups":      []string{"platform-admin", "OPERATOR"},
		}
		tokenStr, err := infra.signer.SignClaims(claimsMap, time.Hour)
		require.NoError(t, err)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := infra.middleware.Handler(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedCtx)

		roles, ok := auth.GetRolesFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, []string{"ADMIN"}, roles)
	})

	t.Run("empty roles and groups yields empty slice", func(t *testing.T) {
		claimsMap := map[string]interface{}{
			"sub":         uuid.New().String(),
			"x-tenant-id": tenantAlpha,
			"email":       "noroles@example.com",
		}
		tokenStr, err := infra.signer.SignClaims(claimsMap, time.Hour)
		require.NoError(t, err)

		var capturedCtx context.Context
		nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
		})

		handler := infra.middleware.Handler(nextHandler)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil)
		req.Header.Set("Authorization", "Bearer "+tokenStr)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, capturedCtx)

		roles, ok := auth.GetRolesFromContext(capturedCtx)
		assert.True(t, ok)
		assert.Empty(t, roles)
	})
}
