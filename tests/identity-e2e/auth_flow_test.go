//go:build integration
// +build integration

package identitye2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identitysvc "github.com/meridianhub/meridian/services/identity/service"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const e2eTenantID = "e2e_auth_test_tenant"

// testSigningKey holds a generated RSA key pair for signing JWTs in tests.
type testSigningKey struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// staticKeyValidator implements gateway auth.JWTValidator using a static RSA public key.
// This lets us test the gateway middleware pipeline without spinning up Dex/OIDC.
type staticKeyValidator struct {
	validator *platformauth.JWTValidator
}

func (v *staticKeyValidator) ValidateToken(tokenString string) (*platformauth.Claims, error) {
	return v.validator.ValidateToken(tokenString)
}

// e2eInfra holds all test infrastructure for identity E2E tests.
type e2eInfra struct {
	db         *gorm.DB
	ctx        context.Context // tenant-scoped context
	svc        *identitysvc.Service
	signingKey *testSigningKey
	middleware *auth.JWTMiddleware
	tenantAuth *auth.TenantAuthorizationMiddleware
	logger     *slog.Logger
}

// setupE2EInfra creates all infrastructure needed for E2E auth flow tests:
// CockroachDB, identity service, RSA signing keys, and gateway JWT middleware.
func setupE2EInfra(t *testing.T) *e2eInfra {
	t.Helper()

	infra := &e2eInfra{}
	infra.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	// Set up CockroachDB testcontainer
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	infra.db = db

	// Limit connection pool for test stability
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Set up tenant schema
	tc := testdb.SetupTenantSchema(t, db, e2eTenantID)
	t.Cleanup(tc.Cleanup)

	// Apply identity service schema DDL
	applyIdentityMigrations(t, db)

	// Store tenant context
	infra.ctx = tc.Ctx

	// Create identity service
	repo := persistence.NewRepository(db)
	svc, err := identitysvc.NewService(repo, infra.logger)
	require.NoError(t, err)
	infra.svc = svc

	// Generate RSA signing key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	infra.signingKey = &testSigningKey{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}

	// Create JWT validator using our test public key
	platformValidator, err := platformauth.NewJWTValidator(infra.signingKey.publicKey)
	require.NoError(t, err)
	validator := &staticKeyValidator{validator: platformValidator}

	// Create gateway JWT middleware
	middleware, err := auth.NewJWTMiddleware(validator, infra.logger)
	require.NoError(t, err)
	infra.middleware = middleware

	// Create tenant authorization middleware
	infra.tenantAuth = auth.NewTenantAuthorizationMiddleware(infra.logger)

	t.Cleanup(func() {
		cleanup()
	})

	return infra
}

// applyIdentityMigrations creates the identity service tables in the current schema.
func applyIdentityMigrations(t *testing.T, db *gorm.DB) {
	t.Helper()

	ddls := []string{
		`CREATE TABLE IF NOT EXISTS identity (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email VARCHAR(255) NOT NULL,
			status VARCHAR(30) NOT NULL DEFAULT 'PENDING_INVITE',
			password_hash VARCHAR(255) NOT NULL DEFAULT '',
			external_idp VARCHAR(100) NOT NULL DEFAULT '',
			external_sub VARCHAR(255) NOT NULL DEFAULT '',
			failed_attempts INT NOT NULL DEFAULT 0,
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			deleted_at TIMESTAMP WITH TIME ZONE,
			UNIQUE (email) WHERE deleted_at IS NULL
		)`,
		`CREATE TABLE IF NOT EXISTS role_assignment (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			identity_id UUID NOT NULL,
			granted_by UUID NOT NULL,
			role VARCHAR(50) NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE,
			revoked_at TIMESTAMP WITH TIME ZONE,
			revoked_by UUID,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS invitation (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			identity_id UUID NOT NULL,
			invited_by UUID NOT NULL,
			token_hash VARCHAR(64) NOT NULL UNIQUE,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
			updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
		)`,
	}

	for _, ddl := range ddls {
		err := db.Exec(ddl).Error
		require.NoError(t, err, "failed to execute DDL: %s", ddl[:min(len(ddl), 80)])
	}
}

// signJWT creates a signed JWT token with the given claims using the test RSA key.
func (infra *e2eInfra) signJWT(t *testing.T, claims *platformauth.Claims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(infra.signingKey.privateKey)
	require.NoError(t, err)
	return tokenString
}

// contextWithAuth injects a caller identity and roles into a context (for identity service calls).
func contextWithAuth(ctx context.Context, callerID uuid.UUID, roles []string) context.Context {
	ctx = context.WithValue(ctx, platformauth.UserIDContextKey, callerID.String())
	ctx = context.WithValue(ctx, platformauth.RolesContextKey, roles)
	return ctx
}

// errorBody is used to decode JSON error responses from the gateway middleware.
type errorBody struct {
	Error string `json:"error"`
}

// =============================================================================
// Test 1: Full Authentication Flow - Create, Invite, Accept, Authenticate, JWT
// =============================================================================

func TestE2E_AuthFlow_CreateAndAuthenticate(t *testing.T) {
	infra := setupE2EInfra(t)

	inviterID := uuid.New()
	authCtx := contextWithAuth(infra.ctx, inviterID, []string{"TENANT_OWNER"})

	// Step 1: Invite user via identity service
	inviteResp, err := infra.svc.InviteUser(authCtx, &pb.InviteUserRequest{
		Email: "e2e-user@test.meridian.dev",
	})
	require.NoError(t, err)
	require.NotNil(t, inviteResp.Identity)
	identityID := inviteResp.Identity.Id
	plaintextToken := inviteResp.InvitationToken

	// Step 2: Accept invitation (sets password + activates)
	acceptResp, err := infra.svc.AcceptInvitation(infra.ctx, &pb.AcceptInvitationRequest{
		Token:    plaintextToken,
		Password: "E2eTestPass123!",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_ACTIVE, acceptResp.Identity.Status)

	// Step 3: Authenticate via identity service
	authResp, err := infra.svc.Authenticate(infra.ctx, &pb.AuthenticateRequest{
		Email:    "e2e-user@test.meridian.dev",
		Password: "E2eTestPass123!",
	})
	require.NoError(t, err)
	assert.True(t, authResp.Authenticated)
	assert.Equal(t, identityID, authResp.Identity.Id)

	// Step 4: Create a JWT with claims matching what Dex would issue
	signedToken := infra.signJWT(t, &platformauth.Claims{
		TenantID: e2eTenantID,
		Roles:    []string{"OPERATOR"},
		Email:    "e2e-user@test.meridian.dev",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   identityID,
			Issuer:    "test-issuer",
			Audience:  jwt.ClaimStrings{"meridian"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})

	// Step 5: Send HTTP request through gateway JWT middleware
	var capturedCtx context.Context
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
	})

	handler := infra.middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Step 6: Verify middleware accepted the token
	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedCtx, "next handler should have been called")

	// Verify claims were injected into context
	userID, ok := auth.GetUserIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, identityID, userID, "user ID should be the identity subject")

	tenantID, ok := auth.GetTenantIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, e2eTenantID, tenantID)

	roles, ok := auth.GetRolesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, []string{"OPERATOR"}, roles)

	extractedClaims, ok := auth.GetClaimsFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, "e2e-user@test.meridian.dev", extractedClaims.Email)

	// Verify tenant package context integration
	tenantFromPkg, ok := tenant.FromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, tenant.TenantID(e2eTenantID), tenantFromPkg)
}

// =============================================================================
// Test 2: Subdomain Security - Tenant Mismatch
// =============================================================================

func TestE2E_SubdomainSecurity_TenantMismatch(t *testing.T) {
	infra := setupE2EInfra(t)

	tenantA := "tenant_alpha"
	tenantB := "tenant_bravo"

	// Create a JWT for tenant A
	signedToken := infra.signJWT(t, &platformauth.Claims{
		TenantID: tenantA,
		Roles:    []string{"OPERATOR"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})

	// Chain: JWT middleware -> Tenant Authorization middleware
	// The tenant authorization middleware checks that JWT tenant matches the resolved tenant.
	var nextCalled bool
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		nextCalled = true
	})

	// Build the middleware chain: tenant_authorization wraps the final handler,
	// jwt middleware wraps that.
	tenantAuthHandler := infra.tenantAuth.Handler(nextHandler)

	// We need to simulate "resolved tenant" in context.
	// In production, the subdomain middleware sets this.
	// Here we inject tenant B to simulate a mismatch.
	tenantInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tidB, err := tenant.NewTenantID(tenantB)
		require.NoError(t, err)
		ctx := tenant.WithTenant(r.Context(), tidB)
		tenantAuthHandler.ServeHTTP(w, r.WithContext(ctx))
	})

	handler := infra.middleware.Handler(tenantInjector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Verify: 403 Forbidden due to tenant mismatch
	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.False(t, nextCalled, "downstream handler must not be called on tenant mismatch")

	var body errorBody
	err := json.NewDecoder(rr.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body.Error, "not authorized for this tenant")

	// Now test with matching tenant - should succeed
	t.Run("matching tenant passes through", func(t *testing.T) {
		var matchedCtx context.Context
		matchNextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			matchedCtx = r.Context()
		})

		matchTenantAuthHandler := infra.tenantAuth.Handler(matchNextHandler)

		// Inject tenant A (matching the JWT)
		matchTenantInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tidA, err := tenant.NewTenantID(tenantA)
			require.NoError(t, err)
			ctx := tenant.WithTenant(r.Context(), tidA)
			matchTenantAuthHandler.ServeHTTP(w, r.WithContext(ctx))
		})

		matchHandler := infra.middleware.Handler(matchTenantInjector)

		matchReq := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
		matchReq.Header.Set("Authorization", "Bearer "+signedToken)
		matchRR := httptest.NewRecorder()

		matchHandler.ServeHTTP(matchRR, matchReq)

		assert.Equal(t, http.StatusOK, matchRR.Code)
		require.NotNil(t, matchedCtx)

		tenantID, ok := auth.GetTenantIDFromContext(matchedCtx)
		assert.True(t, ok)
		assert.Equal(t, tenantA, tenantID)
	})
}

// =============================================================================
// Test 3: Role Claim Lifecycle
// =============================================================================

func TestE2E_RoleClaimLifecycle(t *testing.T) {
	infra := setupE2EInfra(t)

	ownerID := uuid.New()
	ownerCtx := contextWithAuth(infra.ctx, ownerID, []string{"TENANT_OWNER"})

	// Step 1: Invite and activate user
	inviteResp, err := infra.svc.InviteUser(ownerCtx, &pb.InviteUserRequest{
		Email: "role-lifecycle@test.meridian.dev",
	})
	require.NoError(t, err)
	identityID := inviteResp.Identity.Id

	_, err = infra.svc.AcceptInvitation(infra.ctx, &pb.AcceptInvitationRequest{
		Token:    inviteResp.InvitationToken,
		Password: "RoleTest123!",
	})
	require.NoError(t, err)

	// Step 2: Grant OPERATOR role
	_, err = infra.svc.GrantRole(ownerCtx, &pb.GrantRoleRequest{
		IdentityId: identityID,
		Role:       pb.Role_ROLE_OPERATOR,
	})
	require.NoError(t, err)

	// Step 3: Authenticate and verify roles in identity response
	authResp, err := infra.svc.Authenticate(infra.ctx, &pb.AuthenticateRequest{
		Email:    "role-lifecycle@test.meridian.dev",
		Password: "RoleTest123!",
	})
	require.NoError(t, err)
	assert.True(t, authResp.Authenticated)

	// Verify OPERATOR role exists
	listResp, err := infra.svc.ListRoleAssignments(infra.ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: identityID,
	})
	require.NoError(t, err)
	require.Len(t, listResp.RoleAssignments, 1)
	assert.Equal(t, pb.Role_ROLE_OPERATOR, listResp.RoleAssignments[0].Role)

	// Step 4: Grant ADMIN role
	_, err = infra.svc.GrantRole(ownerCtx, &pb.GrantRoleRequest{
		IdentityId: identityID,
		Role:       pb.Role_ROLE_ADMIN,
	})
	require.NoError(t, err)

	// Step 5: Verify both roles returned
	listResp2, err := infra.svc.ListRoleAssignments(infra.ctx, &pb.ListRoleAssignmentsRequest{
		IdentityId: identityID,
	})
	require.NoError(t, err)
	require.Len(t, listResp2.RoleAssignments, 2)

	roleSet := make(map[pb.Role]bool)
	for _, ra := range listResp2.RoleAssignments {
		roleSet[ra.Role] = true
	}
	assert.True(t, roleSet[pb.Role_ROLE_OPERATOR], "should have OPERATOR role")
	assert.True(t, roleSet[pb.Role_ROLE_ADMIN], "should have ADMIN role")

	// Step 6: Create JWT with both roles and verify through gateway middleware
	signedToken := infra.signJWT(t, &platformauth.Claims{
		TenantID: e2eTenantID,
		Roles:    []string{"OPERATOR", "ADMIN"},
		Email:    "role-lifecycle@test.meridian.dev",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   identityID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})

	var capturedCtx context.Context
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
	})

	handler := infra.middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/identities", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedCtx)

	roles, ok := auth.GetRolesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Contains(t, roles, "OPERATOR")
	assert.Contains(t, roles, "ADMIN")
}

// =============================================================================
// Test 4: Invalid and Expired Token Rejection
// =============================================================================

func TestE2E_TokenRejection(t *testing.T) {
	infra := setupE2EInfra(t)

	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("next handler should not be called for rejected tokens")
	})
	handler := infra.middleware.Handler(nextHandler)

	t.Run("expired token returns 401", func(t *testing.T) {
		expiredToken := infra.signJWT(t, &platformauth.Claims{
			TenantID: e2eTenantID,
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   uuid.New().String(),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.Header.Set("Authorization", "Bearer "+expiredToken)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "token expired")
	})

	t.Run("wrong signing key returns 401", func(t *testing.T) {
		// Generate a different RSA key
		otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)

		claims := &platformauth.Claims{
			TenantID: e2eTenantID,
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   uuid.New().String(),
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		wrongToken, err := token.SignedString(otherKey)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.Header.Set("Authorization", "Bearer "+wrongToken)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("missing auth header returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
		assert.Contains(t, rr.Body.String(), "missing authorization header")
	})

	t.Run("malformed bearer token returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.Header.Set("Authorization", "Bearer not.a.real.jwt.at.all")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

// =============================================================================
// Test 5: OIDC Subject Fallback (EffectiveUserID)
// =============================================================================

func TestE2E_OIDCSubjectFallback(t *testing.T) {
	infra := setupE2EInfra(t)

	subjectID := uuid.New().String()

	// Create a JWT without UserID but with Subject (like Dex would issue).
	// The EffectiveUserID should fall back to Subject.
	signedToken := infra.signJWT(t, &platformauth.Claims{
		// UserID intentionally empty - relying on Subject
		TenantID: e2eTenantID,
		Email:    "oidc-user@test.meridian.dev",
		Roles:    []string{"OPERATOR"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subjectID,
			Issuer:    "https://dex.test.meridian.dev",
			Audience:  jwt.ClaimStrings{"meridian"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})

	var capturedCtx context.Context
	nextHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
	})

	handler := infra.middleware.Handler(nextHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/identities/me", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedCtx)

	// The middleware uses EffectiveUserID which falls back to Subject
	userID, ok := auth.GetUserIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, subjectID, userID, "should fall back to JWT subject when UserID is empty")

	claims, ok := auth.GetClaimsFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, "oidc-user@test.meridian.dev", claims.Email)
}

// =============================================================================
// Test 6: Full Pipeline - JWT Auth + Tenant Auth Matching
// =============================================================================

func TestE2E_FullPipeline_JWTAndTenantAuth(t *testing.T) {
	infra := setupE2EInfra(t)

	targetTenant := e2eTenantID
	identityID := uuid.New().String()

	signedToken := infra.signJWT(t, &platformauth.Claims{
		TenantID: targetTenant,
		Roles:    []string{"ADMIN"},
		Email:    "pipeline-user@test.meridian.dev",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   identityID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})

	var capturedCtx context.Context
	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = r.Context()
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Build the full chain: JWT -> tenant inject -> tenant auth -> handler
	tenantAuthHandler := infra.tenantAuth.Handler(finalHandler)

	tenantInjector := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid, err := tenant.NewTenantID(targetTenant)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid tenant: %v", err), http.StatusBadRequest)
			return
		}
		ctx := tenant.WithTenant(r.Context(), tid)
		tenantAuthHandler.ServeHTTP(w, r.WithContext(ctx))
	})

	handler := infra.middleware.Handler(tenantInjector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/positions", nil)
	req.Header.Set("Authorization", "Bearer "+signedToken)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Verify the full pipeline passed
	assert.Equal(t, http.StatusOK, rr.Code)
	require.NotNil(t, capturedCtx)

	// Verify all context values are set correctly after the full chain
	userID, ok := auth.GetUserIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, identityID, userID)

	tenantID, ok := auth.GetTenantIDFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, targetTenant, tenantID)

	roles, ok := auth.GetRolesFromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, []string{"ADMIN"}, roles)

	// Verify tenant package context is set (used by downstream services)
	tenantFromPkg, ok := tenant.FromContext(capturedCtx)
	assert.True(t, ok)
	assert.Equal(t, tenant.TenantID(targetTenant), tenantFromPkg)

	// Verify JSON response body
	var responseBody map[string]string
	err := json.NewDecoder(rr.Body).Decode(&responseBody)
	require.NoError(t, err)
	assert.Equal(t, "ok", responseBody["status"])
}
