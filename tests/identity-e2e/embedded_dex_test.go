//go:build integration
// +build integration

package identitye2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identityconnector "github.com/meridianhub/meridian/services/identity/connector"
	identitydex "github.com/meridianhub/meridian/services/identity/dex"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dexTestTenantID = "dex_e2e_test_tenant"

// dexE2EInfra holds infrastructure for embedded Dex integration tests.
type dexE2EInfra struct {
	ctx       context.Context // tenant-scoped context
	repo      domain.Repository
	dexServer *httptest.Server
	issuer    string
	clientID  string
	logger    *slog.Logger
}

// setupDexE2EInfra creates all infrastructure needed for embedded Dex E2E tests:
// CockroachDB, identity repository, embedded Dex OIDC server running on httptest.
func setupDexE2EInfra(t *testing.T) *dexE2EInfra {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Set up CockroachDB testcontainer.
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	// Set up tenant schema.
	tc := testdb.SetupTenantSchema(t, db, dexTestTenantID)
	t.Cleanup(tc.Cleanup)

	// Apply identity service schema DDL.
	applyIdentityMigrations(t, db)

	tenantCtx := tc.Ctx

	// Create identity repository.
	repo := persistence.NewRepository(db)

	// Create Meridian connector for Dex.
	conn, err := identityconnector.New(repo, logger)
	require.NoError(t, err)

	// We need a placeholder issuer URL; we'll update after starting the test server.
	// Use a two-phase approach: create a mux, start the server, then start Dex with the real issuer.
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	issuer := server.URL + "/dex"
	clientID := "test-e2e-client"

	embedded, err := identitydex.New(context.Background(), identitydex.Config{
		Issuer:    issuer,
		Connector: conn,
		Logger:    logger,
		Clients: []identitydex.ClientConfig{
			{
				ID:           clientID,
				Public:       true,
				RedirectURIs: []string{server.URL + "/callback"},
				Name:         "E2E Test Client",
			},
		},
	})
	require.NoError(t, err)

	err = embedded.StartServer(context.Background(), issuer, true)
	require.NoError(t, err)

	dexHandler := embedded.Handler()
	require.NotNil(t, dexHandler)

	// Mount the Dex handler at /dex/ with tenant context injection.
	// In production, the tenant resolver middleware does this from the subdomain.
	// In tests, we inject tenant context directly.
	mux.Handle("/dex/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid, err := tenant.NewTenantID(dexTestTenantID)
		if err != nil {
			http.Error(w, "invalid tenant", http.StatusInternalServerError)
			return
		}
		ctx := tenant.WithTenant(r.Context(), tid)
		dexHandler.ServeHTTP(w, r.WithContext(ctx))
	}))

	return &dexE2EInfra{
		ctx:       tenantCtx,
		repo:      repo,
		dexServer: server,
		issuer:    issuer,
		clientID:  clientID,
		logger:    logger,
	}
}

// seedUser creates an active identity with a password and role in the test tenant.
func (infra *dexE2EInfra) seedUser(t *testing.T, email, password, role string) uuid.UUID {
	t.Helper()

	hash, err := credentials.HashPassword(password)
	require.NoError(t, err)

	identity, err := domain.NewIdentity(email)
	require.NoError(t, err)

	err = identity.SetPassword(hash)
	require.NoError(t, err)

	err = identity.Activate()
	require.NoError(t, err)

	now := time.Now()
	ra := domain.ReconstructRoleAssignment(
		uuid.New(),
		identity.ID(),
		identity.ID(),
		domain.Role(role),
		nil, nil, nil,
		now, now,
	)

	err = infra.repo.SaveIdentityWithRoles(infra.ctx, identity, []*domain.RoleAssignment{ra})
	require.NoError(t, err)

	return identity.ID()
}

// seedLockedUser creates an active user then locks the account.
func (infra *dexE2EInfra) seedLockedUser(t *testing.T, email, password string) uuid.UUID {
	t.Helper()

	hash, err := credentials.HashPassword(password)
	require.NoError(t, err)

	identity, err := domain.NewIdentity(email)
	require.NoError(t, err)

	err = identity.SetPassword(hash)
	require.NoError(t, err)

	err = identity.Activate()
	require.NoError(t, err)

	err = identity.Lock()
	require.NoError(t, err)

	err = infra.repo.Save(infra.ctx, identity)
	require.NoError(t, err)

	return identity.ID()
}

// tokenResponse represents the OAuth2 token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// passwordGrant performs an OAuth2 resource owner password credentials grant.
func (infra *dexE2EInfra) passwordGrant(t *testing.T, email, password string) (*http.Response, *tokenResponse) {
	t.Helper()

	data := url.Values{
		"grant_type": {"password"},
		"username":   {email},
		"password":   {password},
		"client_id":  {infra.clientID},
		"scope":      {"openid email profile groups offline_access"},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		infra.issuer+"/token", strings.NewReader(data.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var tokenResp tokenResponse
	err = json.Unmarshal(body, &tokenResp)
	require.NoError(t, err)

	return resp, &tokenResp
}

// =============================================================================
// Test 1: Password grant with valid credentials
// =============================================================================

func TestEmbeddedDex_PasswordGrant_ValidCredentials(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "valid-user@test.meridian.dev"
	password := "ValidPass123!"

	infra.seedUser(t, email, password, "OPERATOR")

	resp, tokenResp := infra.passwordGrant(t, email, password)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, tokenResp.Error, "expected no error, got: %s", tokenResp.ErrorDesc)
	assert.NotEmpty(t, tokenResp.AccessToken, "access_token should be present")
	assert.NotEmpty(t, tokenResp.IDToken, "id_token should be present")
	assert.Equal(t, "bearer", strings.ToLower(tokenResp.TokenType))
	assert.Greater(t, tokenResp.ExpiresIn, 0)
}

// =============================================================================
// Test 2: Use token for authenticated API call via JWKS validation
// =============================================================================

func TestEmbeddedDex_TokenUsableForAPIAuth(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "api-auth@test.meridian.dev"
	password := "ApiAuth123!"

	infra.seedUser(t, email, password, "OPERATOR")

	_, tokenResp := infra.passwordGrant(t, email, password)
	require.NotEmpty(t, tokenResp.IDToken)

	// Create a JWKS provider pointing at the embedded Dex JWKS endpoint.
	ctx := context.Background()
	jwksProvider, err := platformauth.NewJWKSProvider(ctx, &platformauth.JWKSProviderConfig{
		URL:      infra.issuer + "/keys",
		Client:   http.DefaultClient,
		CacheTTL: time.Hour,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = jwksProvider.Close() })

	// Validate the ID token via JWKS.
	validator, err := platformauth.NewJWTValidatorWithJWKS(jwksProvider)
	require.NoError(t, err)

	claims, err := validator.ValidateToken(ctx, tokenResp.IDToken)
	require.NoError(t, err, "JWKS-validated token should be accepted")
	assert.Equal(t, email, claims.Email)
}

// =============================================================================
// Test 3: Verify JWT claims from password grant
// =============================================================================

func TestEmbeddedDex_VerifyJWTClaims(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "claims-user@test.meridian.dev"
	password := "ClaimsPass123!"

	identityID := infra.seedUser(t, email, password, "OPERATOR")

	_, tokenResp := infra.passwordGrant(t, email, password)
	require.NotEmpty(t, tokenResp.IDToken)

	// Parse the ID token without full validation to inspect all claims.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenResp.IDToken, jwt.MapClaims{})
	require.NoError(t, err)

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	require.True(t, ok)

	// Standard OIDC claims.
	assert.Equal(t, infra.issuer, mapClaims["iss"], "issuer should match Dex issuer")

	// Dex encodes the sub claim as a base64-encoded protobuf containing the
	// connector's UserID and the connector ID ("meridian"). Decode and verify
	// the identity UUID is embedded within.
	sub, ok := mapClaims["sub"].(string)
	require.True(t, ok, "sub claim should be a string")
	decodedSub, err := base64.StdEncoding.DecodeString(sub)
	require.NoError(t, err, "sub claim should be valid base64")
	assert.Contains(t, string(decodedSub), identityID.String(),
		"decoded sub should contain the identity UUID")

	assert.Equal(t, email, mapClaims["email"], "email claim should match")
	assert.NotEmpty(t, mapClaims["aud"], "audience should be present")

	// Expiration.
	exp, ok := mapClaims["exp"]
	require.True(t, ok, "exp claim should be present")
	expFloat, ok := exp.(float64)
	require.True(t, ok)
	assert.Greater(t, expFloat, float64(time.Now().Unix()), "token should not be expired")

	// Groups/roles claim (Dex maps connector groups to the "groups" claim).
	if groups, ok := mapClaims["groups"]; ok {
		groupsList, ok := groups.([]interface{})
		if ok && len(groupsList) > 0 {
			assert.Contains(t, groupsList, "OPERATOR", "groups should contain the user's role")
		}
	}
}

// =============================================================================
// Test 4: Invalid credentials return error
// =============================================================================

func TestEmbeddedDex_InvalidCredentials(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "invalid-creds@test.meridian.dev"
	password := "CorrectPass123!"

	infra.seedUser(t, email, password, "OPERATOR")

	t.Run("wrong password", func(t *testing.T) {
		resp, tokenResp := infra.passwordGrant(t, email, "WrongPassword!")

		// Dex returns 401 for invalid password grants.
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, tokenResp.Error)
		assert.Empty(t, tokenResp.AccessToken)
		assert.Empty(t, tokenResp.IDToken)
	})

	t.Run("unknown email", func(t *testing.T) {
		resp, tokenResp := infra.passwordGrant(t, "nonexistent@test.meridian.dev", "SomePass123!")

		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, tokenResp.Error)
		assert.Empty(t, tokenResp.AccessToken)
	})
}

// =============================================================================
// Test 5: Locked account returns error
// =============================================================================

func TestEmbeddedDex_LockedAccount(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "locked-user@test.meridian.dev"
	password := "LockedPass123!"

	infra.seedLockedUser(t, email, password)

	resp, tokenResp := infra.passwordGrant(t, email, password)

	assert.NotEqual(t, http.StatusOK, resp.StatusCode,
		"locked account should not receive a token")
	assert.NotEmpty(t, tokenResp.Error)
	assert.Empty(t, tokenResp.AccessToken)
	assert.Empty(t, tokenResp.IDToken)
}

// =============================================================================
// Test 6: Tenant scoping via password grant
// =============================================================================

func TestEmbeddedDex_TenantScoping(t *testing.T) {
	infra := setupDexE2EInfra(t)
	email := "tenant-user@test.meridian.dev"
	password := "TenantPass123!"

	infra.seedUser(t, email, password, "ADMIN")

	_, tokenResp := infra.passwordGrant(t, email, password)
	require.NotEmpty(t, tokenResp.IDToken)

	// Parse claims and check tenant.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenResp.IDToken, jwt.MapClaims{})
	require.NoError(t, err)

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	require.True(t, ok)

	// Dex does not surface custom connector claims (like x-tenant-id) in the
	// ID token by default. The primary validation here is that the password
	// grant succeeded, which proves the connector resolved credentials within
	// the injected tenant context (dex_e2e_test_tenant schema).

	// Verify the sub claim is present and encodes the seeded identity's UUID.
	sub, ok := mapClaims["sub"].(string)
	require.True(t, ok, "sub claim must be a string")
	decodedSub, err := base64.StdEncoding.DecodeString(sub)
	require.NoError(t, err)
	assert.NotEmpty(t, decodedSub, "decoded sub should be non-empty")

	// Verify the email matches -- confirms the correct identity was resolved.
	assert.Equal(t, email, mapClaims["email"], "email should match seeded user")
}

// =============================================================================
// Test 7: JWKS endpoint returns valid keys
// =============================================================================

func TestEmbeddedDex_JWKSEndpoint(t *testing.T) {
	infra := setupDexE2EInfra(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, infra.issuer+"/keys", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var jwks platformauth.JWKS
	err = json.Unmarshal(body, &jwks)
	require.NoError(t, err, "JWKS response should be valid JSON")
	require.NotEmpty(t, jwks.Keys, "JWKS should contain at least one key")

	for _, key := range jwks.Keys {
		assert.NotEmpty(t, key.Kid, "key ID should be present")
		assert.Equal(t, "RSA", key.Kty, "key type should be RSA")
		assert.NotEmpty(t, key.N, "modulus should be present")
		assert.NotEmpty(t, key.E, "exponent should be present")
	}
}

// =============================================================================
// Test 8: OIDC discovery endpoint
// =============================================================================

func TestEmbeddedDex_OIDCDiscovery(t *testing.T) {
	infra := setupDexE2EInfra(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		infra.issuer+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var discovery map[string]interface{}
	err = json.Unmarshal(body, &discovery)
	require.NoError(t, err, "discovery doc should be valid JSON")

	// Verify standard OIDC discovery fields.
	assert.Equal(t, infra.issuer, discovery["issuer"],
		"issuer in discovery should match configured issuer")
	assert.NotEmpty(t, discovery["authorization_endpoint"])
	assert.NotEmpty(t, discovery["token_endpoint"])
	assert.NotEmpty(t, discovery["jwks_uri"])

	// JWKS URI should point to our keys endpoint.
	jwksURI, ok := discovery["jwks_uri"].(string)
	require.True(t, ok)
	assert.Equal(t, infra.issuer+"/keys", jwksURI)

	// Check supported response types and grant types.
	if responseTypes, ok := discovery["response_types_supported"]; ok {
		assert.NotEmpty(t, responseTypes)
	}

	// Verify the signing algorithms.
	if algos, ok := discovery["id_token_signing_alg_values_supported"]; ok {
		algoList, ok := algos.([]interface{})
		if ok {
			assert.Contains(t, algoList, "RS256")
		}
	}
}
