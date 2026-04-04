package gateway

// auth_flows_test.go — Integration regression guard for all three auth flows.
// Prevents the circular fix pattern described in PRD 044 where fixing one auth
// flow breaks another due to shared tenant resolution.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/identity/connector"
	tdomain "github.com/meridianhub/meridian/services/tenant/domain"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test infrastructure — shared across auth flow tests
// ---------------------------------------------------------------------------

const authFlowBaseDomain = "api.example.com"

// authFlowSignerAndValidator creates a JWTSigner and JWTValidator pair.
func authFlowSignerAndValidator(t *testing.T) (*platformauth.JWTSigner, *platformauth.JWTValidator) {
	t.Helper()
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		KeyID:  "test-auth-flows-1",
		Issuer: "test-meridian",
	})
	require.NoError(t, err)
	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	return signer, validator
}

// authFlowFixture holds a fully-wired gateway server with all three auth flows.
type authFlowFixture struct {
	server    *Server
	signer    *platformauth.JWTSigner
	validator *platformauth.JWTValidator
}

// authFlowSlugCache satisfies the slugCache interface for TenantResolverMiddleware.
type authFlowSlugCache struct {
	entries map[string]tenant.TenantID
}

func (c *authFlowSlugCache) Get(_ context.Context, slug string) (tenant.TenantID, string, error) {
	id, ok := c.entries[slug]
	if !ok {
		return "", "", nil
	}
	return id, "", nil
}

func (c *authFlowSlugCache) Set(_ context.Context, slug string, tenantID tenant.TenantID, _ string) error {
	c.entries[slug] = tenantID
	return nil
}

func (c *authFlowSlugCache) Invalidate(_ context.Context, _ string) {}

// authFlowTenantRepo satisfies the tenantRepository interface for TenantResolverMiddleware.
type authFlowTenantRepo struct {
	tenants map[string]*tdomain.Tenant
}

func (r *authFlowTenantRepo) GetBySlug(_ context.Context, slug string) (*tdomain.Tenant, error) {
	t, ok := r.tenants[slug]
	if !ok {
		return nil, tdomain.ErrNotFound
	}
	return t, nil
}

// authFlowConnector is a test password connector.
type authFlowConnector struct {
	loginFn func(ctx context.Context, scopes []string, username, password string) (connector.Identity, bool, error)
}

func (c *authFlowConnector) Login(ctx context.Context, scopes []string, username, password string) (connector.Identity, bool, error) {
	return c.loginFn(ctx, scopes, username, password)
}

// authFlowIdentityResolver is a test identity resolver for SSO.
type authFlowIdentityResolver struct {
	resolveFn func(ctx context.Context, email string) (connector.Identity, bool, error)
}

func (r *authFlowIdentityResolver) Resolve(ctx context.Context, email string) (connector.Identity, bool, error) {
	return r.resolveFn(ctx, email)
}

// authFlowMultiTenantResolver creates a TenantResolverMiddleware with multiple tenants.
func authFlowMultiTenantResolver(t *testing.T, baseDomain string, tenants map[string]tenant.TenantID) *platformgateway.TenantResolverMiddleware {
	t.Helper()
	cache := &authFlowSlugCache{entries: make(map[string]tenant.TenantID)}
	repo := &authFlowTenantRepo{tenants: make(map[string]*tdomain.Tenant)}
	for slug, id := range tenants {
		repo.tenants[slug] = &tdomain.Tenant{
			ID:     id,
			Slug:   slug,
			Status: tdomain.StatusActive,
		}
	}
	resolver, err := platformgateway.NewTenantResolverMiddleware(
		cache, repo, baseDomain, slog.New(slog.NewTextHandler(io.Discard, nil)), false,
	)
	require.NoError(t, err)
	return resolver
}

// newAuthFlowFixture creates a server with BFF login, SSO, and Dex handlers configured.
func newAuthFlowFixture(t *testing.T) *authFlowFixture {
	t.Helper()

	signer, validator := authFlowSignerAndValidator(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Two tenants for isolation testing
	acmeTID := tenant.MustNewTenantID("acme_corp")
	betaTID := tenant.MustNewTenantID("beta_org")

	resolver := authFlowMultiTenantResolver(t, authFlowBaseDomain, map[string]tenant.TenantID{
		"acme": acmeTID,
		"beta": betaTID,
	})

	// BFF password login handler
	conn := &authFlowConnector{
		loginFn: func(_ context.Context, _ []string, email, password string) (connector.Identity, bool, error) {
			if email == "alice@acme.com" && password == "secret" {
				return connector.Identity{
					UserID:   "user-alice",
					Username: "Alice",
					Email:    "alice@acme.com",
					Groups:   []string{"operator"},
				}, true, nil
			}
			if email == "bob@beta.org" && password == "secret" {
				return connector.Identity{
					UserID:   "user-bob",
					Username: "Bob",
					Email:    "bob@beta.org",
					Groups:   []string{"viewer"},
				}, true, nil
			}
			return connector.Identity{}, false, nil
		},
	}
	authHandler, err := NewAuthHandler(AuthHandlerConfig{
		Connector: conn,
		Signer:    signer,
		Logger:    logger,
	})
	require.NoError(t, err)

	// BFF SSO handler
	ssoResolver := &authFlowIdentityResolver{
		resolveFn: func(_ context.Context, email string) (connector.Identity, bool, error) {
			if email == "alice@acme.com" {
				return connector.Identity{
					UserID: "user-alice", Username: "Alice", Email: email, Groups: []string{"operator"},
				}, true, nil
			}
			return connector.Identity{}, false, nil
		},
	}
	ssoHandler, err := NewSSOHandler(SSOHandlerConfig{
		DexIssuerURL: "https://" + authFlowBaseDomain + "/dex",
		ClientID:     "meridian-service",
		CallbackURL:  "https://" + authFlowBaseDomain + "/api/auth/callback",
		BaseDomain:   authFlowBaseDomain,
		Signer:       signer,
		Resolver:     ssoResolver,
		Logger:       logger,
		StateStore:   NewStateStore(5 * time.Minute),
	})
	require.NoError(t, err)

	// Fake Dex handler — responds to /dex/* with JWKS-style response
	fakeDex := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})

	config := &Config{
		Port:        8080,
		BaseDomain:  authFlowBaseDomain,
		DatabaseURL: "postgres://localhost/test",
	}

	server := NewServer(config, logger, resolver,
		WithAuthHandler(authHandler),
		WithSSOHandler(ssoHandler),
		WithDexHandler(fakeDex),
	)

	return &authFlowFixture{
		server:    server,
		signer:    signer,
		validator: validator,
	}
}

// serve sends a request through the server's mux and returns the recorder.
func (f *authFlowFixture) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.server.mux.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Flow 1: BFF Password Login
// ---------------------------------------------------------------------------

func TestAuthFlow_BFFPasswordLogin_ValidCredentials(t *testing.T) {
	f := newAuthFlowFixture(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@acme.com",
		"password": "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "acme." + authFlowBaseDomain

	rec := f.serve(req)

	require.Equal(t, http.StatusOK, rec.Code, "login should succeed with valid credentials")

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEmpty(t, resp["access_token"], "response must contain access_token")
	assert.Equal(t, "Bearer", resp["token_type"])

	// Validate JWT has correct tenant UUID claim
	claims, err := f.validator.ValidateToken(resp["access_token"].(string))
	require.NoError(t, err)
	assert.Equal(t, "alice@acme.com", claims.Email)
	assert.Equal(t, "acme_corp", claims.TenantID, "JWT must contain tenant UUID from resolver")
	assert.Contains(t, claims.Roles, "operator")
}

func TestAuthFlow_BFFPasswordLogin_NoTenantSubdomain(t *testing.T) {
	f := newAuthFlowFixture(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@acme.com",
		"password": "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = authFlowBaseDomain // bare domain, no subdomain

	rec := f.serve(req)

	// The tenant resolver middleware returns 404 for bare domain (no subdomain).
	// The login handler never executes — the middleware short-circuits.
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"login without tenant subdomain should fail with 404 from tenant resolver")
}

// ---------------------------------------------------------------------------
// Flow 2: BFF SSO Initiate
// ---------------------------------------------------------------------------

func TestAuthFlow_BFFSSOInitiate_RedirectsToTenantScopedDex(t *testing.T) {
	f := newAuthFlowFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/meridian", nil)
	req.Host = "acme." + authFlowBaseDomain

	rec := f.serve(req)

	require.Equal(t, http.StatusFound, rec.Code, "SSO initiate should redirect")

	location := rec.Header().Get("Location")
	require.NotEmpty(t, location)

	u, err := url.Parse(location)
	require.NoError(t, err)

	assert.Equal(t, "acme."+authFlowBaseDomain, u.Host,
		"redirect must include tenant subdomain in Dex URL")
	assert.Equal(t, "/dex/auth/meridian", u.Path)
	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "meridian-service", u.Query().Get("client_id"))
	assert.NotEmpty(t, u.Query().Get("state"))
	assert.NotEmpty(t, u.Query().Get("code_challenge"))
	assert.Equal(t, "S256", u.Query().Get("code_challenge_method"))
}

func TestAuthFlow_BFFSSOInitiate_NoTenantFails(t *testing.T) {
	f := newAuthFlowFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/meridian", nil)
	req.Host = authFlowBaseDomain // bare domain

	rec := f.serve(req)

	// Tenant resolver middleware returns 404 for bare domain (no subdomain).
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"SSO initiate without tenant should fail with 404 from tenant resolver")
}

// ---------------------------------------------------------------------------
// Flow 4: Dex Platform Endpoints (No Tenant Required)
// ---------------------------------------------------------------------------

func TestAuthFlow_DexKeys_WithoutSubdomain(t *testing.T) {
	f := newAuthFlowFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/dex/keys", nil)
	req.Host = authFlowBaseDomain // bare domain, no subdomain

	rec := f.serve(req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"/dex/keys must work without tenant subdomain (platform endpoint)")
	assert.Contains(t, rec.Body.String(), "keys",
		"response should contain JWKS keys")
}

func TestAuthFlow_DexAuth_WithTenantSubdomain(t *testing.T) {
	f := newAuthFlowFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/dex/auth/meridian", nil)
	req.Host = "acme." + authFlowBaseDomain

	rec := f.serve(req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"/dex/ endpoints should work with tenant subdomain")
}

// ---------------------------------------------------------------------------
// Flow 5: Cross-Flow Token Validity
// ---------------------------------------------------------------------------

func TestAuthFlow_CrossFlow_TokenSignedByAnyFlowValidatedBySameKey(t *testing.T) {
	signer, validator := authFlowSignerAndValidator(t)

	// Simulate a JWT as produced by the BFF login handler
	claims := map[string]interface{}{
		"sub":         "user-alice",
		"email":       "alice@acme.com",
		"name":        "Alice",
		"x-tenant-id": "acme_corp",
		"roles":       []string{"operator"},
	}
	token, err := signer.SignClaims(claims, time.Hour)
	require.NoError(t, err)

	// Validate with the same public key (auth middleware uses the same JWKS)
	parsed, err := validator.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "alice@acme.com", parsed.Email)
	assert.Equal(t, "acme_corp", parsed.TenantID)
}

// ---------------------------------------------------------------------------
// Flow 6: Multi-Tenant Isolation
// ---------------------------------------------------------------------------

func TestAuthFlow_MultiTenant_LoginIsolation(t *testing.T) {
	f := newAuthFlowFixture(t)

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@acme.com",
		"password": "secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "acme." + authFlowBaseDomain

	rec := f.serve(req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	claims, err := f.validator.ValidateToken(resp["access_token"].(string))
	require.NoError(t, err)

	assert.Equal(t, "acme_corp", claims.TenantID,
		"token must be scoped to the tenant the user logged into")
	assert.NotEqual(t, "beta_org", claims.TenantID)
}

func TestAuthFlow_MultiTenant_DifferentTenantsGetDifferentTokens(t *testing.T) {
	f := newAuthFlowFixture(t)

	// Alice logs into acme
	body1, _ := json.Marshal(map[string]string{
		"email": "alice@acme.com", "password": "secret",
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	req1.Host = "acme." + authFlowBaseDomain
	rec1 := f.serve(req1)
	require.Equal(t, http.StatusOK, rec1.Code)

	var resp1 map[string]interface{}
	require.NoError(t, json.NewDecoder(rec1.Body).Decode(&resp1))
	claims1, err := f.validator.ValidateToken(resp1["access_token"].(string))
	require.NoError(t, err)

	// Bob logs into beta
	body2, _ := json.Marshal(map[string]string{
		"email": "bob@beta.org", "password": "secret",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Host = "beta." + authFlowBaseDomain
	rec2 := f.serve(req2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var resp2 map[string]interface{}
	require.NoError(t, json.NewDecoder(rec2.Body).Decode(&resp2))
	claims2, err := f.validator.ValidateToken(resp2["access_token"].(string))
	require.NoError(t, err)

	assert.Equal(t, "acme_corp", claims1.TenantID)
	assert.Equal(t, "beta_org", claims2.TenantID)
	assert.NotEqual(t, claims1.TenantID, claims2.TenantID,
		"tokens for different tenants must have different tenant IDs")
}

// ---------------------------------------------------------------------------
// SSO redirect URL construction — multiple connectors
// ---------------------------------------------------------------------------

func TestAuthFlow_BFFSSOInitiate_DifferentConnectors(t *testing.T) {
	f := newAuthFlowFixture(t)

	connectors := []string{"google", "github", "meridian"}
	for _, connID := range connectors {
		t.Run(connID, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/"+connID, nil)
			req.Host = "acme." + authFlowBaseDomain

			rec := f.serve(req)
			require.Equal(t, http.StatusFound, rec.Code)

			location := rec.Header().Get("Location")
			u, err := url.Parse(location)
			require.NoError(t, err)

			assert.Equal(t, "/dex/auth/"+connID, u.Path,
				"redirect path should contain connector ID")
			assert.Equal(t, "acme."+authFlowBaseDomain, u.Host,
				"redirect host should include tenant subdomain")
		})
	}
}

// ---------------------------------------------------------------------------
// Health endpoints unaffected by auth configuration
// ---------------------------------------------------------------------------

func TestAuthFlow_HealthEndpoints_UnaffectedByAuthConfig(t *testing.T) {
	f := newAuthFlowFixture(t)

	endpoints := []string{"/health", "/ready"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			req.Host = ""

			rec := f.serve(req)

			assert.Equal(t, http.StatusOK, rec.Code,
				"health endpoints must work without auth or tenant context")
		})
	}
}
