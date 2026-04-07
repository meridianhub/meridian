package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	gwauth "github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/mcp-server/oauthwiring"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSigner creates a JWT signer with an auto-generated RSA key for testing.
func testSigner(t *testing.T) *platformauth.JWTSigner {
	t.Helper()
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		KeyID:  "test-key-1",
		Issuer: "meridian-test",
	})
	require.NoError(t, err)
	return signer
}

// buildTestGateway creates a gateway server wired with MCP OAuth endpoints
// and a BFF consent handler sharing the same stores.
func buildTestGateway(t *testing.T, signer *platformauth.JWTSigner, baseDomain string) (*gateway.Server, func()) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	consentStore := gateway.NewConsentCodeStore()
	consentConsumer := &consentStoreAdapter{store: consentStore}

	endpoints, mcpCleanup, err := oauthwiring.Wire(oauthwiring.Config{
		Signer:            signer,
		BaseDomain:        baseDomain,
		BaseURL:           "http://localhost:8090",
		ClientID:          "test-mcp-client",
		RedirectURI:       "http://localhost:8090/oauth/callback",
		TokenTTL:          time.Hour,
		DefaultTenantSlug: "test-tenant",
		Logger:            logger,
	}, consentConsumer)
	require.NoError(t, err)

	oidcPeeker := &oidcStatePeekerAdapter{store: endpoints.StateStore}
	consentHandler := gateway.NewMCPConsentHandler(gateway.MCPConsentHandlerConfig{
		ConsentStore:   consentStore,
		OIDCStateStore: oidcPeeker,
		Logger:         logger,
	})

	config := &gateway.Config{
		Port:       0,
		BaseDomain: baseDomain,
	}

	srv := gateway.NewServer(config, logger, nil,
		gateway.WithMCPConsentHandler(consentHandler),
		gateway.WithMCPOAuthEndpoints(&gateway.MCPOAuthEndpoints{
			Authorize:   endpoints.Authorize,
			Callback:    endpoints.Callback,
			Token:       endpoints.Token,
			ConsentInfo: endpoints.ConsentInfo,
			Metadata:    endpoints.Metadata,
			Register:    endpoints.Register,
		}),
	)

	cleanup := func() {
		consentStore.Close()
		if mcpCleanup != nil {
			mcpCleanup()
		}
	}

	return srv, cleanup
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// withAuthClaims injects JWT claims and tenant slug into the request context,
// simulating what the gateway auth middleware chain does.
func withAuthClaims(ctx context.Context, email, tenantID, tenantSlug string) context.Context {
	claims := &platformauth.Claims{
		Email:    email,
		TenantID: tenantID,
	}
	ctx = context.WithValue(ctx, gwauth.ClaimsContextKey, claims)
	ctx = tenant.WithSlug(ctx, tenantSlug)
	return ctx
}

// doGet performs an HTTP GET with context.
func doGet(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

// doPostForm performs an HTTP POST with form values and context.
func doPostForm(ctx context.Context, client *http.Client, url string, data url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return client.Do(req)
}

// doPostJSON performs an HTTP POST with JSON body and context.
func doPostJSON(ctx context.Context, client *http.Client, url, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return client.Do(req)
}

// TestMCPOAuthFullApproveFlow tests the complete consent-based OAuth flow:
// authorize -> consent-info -> approve -> callback -> token exchange.
func TestMCPOAuthFullApproveFlow(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "localhost")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceChallenge(verifier)

	// Step 1: GET /oauth/authorize - should redirect to consent page.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := doGet(ctx, noRedirect, ts.URL+"/oauth/authorize?"+url.Values{
		"client_id":             {"test-mcp-client"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://localhost:8090/oauth/callback"},
		"state":                 {"mcp-client-state"},
		"scope":                 {"mcp:default"},
	}.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode, "authorize should redirect to consent page")

	redirectLocation := resp.Header.Get("Location")
	require.Contains(t, redirectLocation, "/auth/mcp-consent", "should redirect to consent page")

	// Parse the mcp_state from the redirect URL.
	redirectURL, err := url.Parse(redirectLocation)
	require.NoError(t, err)
	mcpState := redirectURL.Query().Get("mcp_state")
	require.NotEmpty(t, mcpState, "mcp_state should be in redirect")

	// Step 2: GET /oauth/consent-info - verify flow metadata.
	infoResp, err := doGet(ctx, http.DefaultClient, ts.URL+"/oauth/consent-info?"+url.Values{
		"client_id": {"test-mcp-client"},
		"mcp_state": {mcpState},
	}.Encode())
	require.NoError(t, err)
	defer infoResp.Body.Close()
	require.Equal(t, http.StatusOK, infoResp.StatusCode)

	var infoBody map[string]interface{}
	require.NoError(t, json.NewDecoder(infoResp.Body).Decode(&infoBody))
	assert.Equal(t, "test-mcp-client", infoBody["client_id"])

	// Step 3: POST /api/auth/mcp-consent (approve).
	// The consent handler is behind auth middleware in production, but in this
	// test there is no auth middleware. The handler reads claims from context,
	// so we call it directly via the mux with injected context.
	consentBody := `{"mcp_state":"` + mcpState + `","client_id":"test-mcp-client","action":"approve"}`
	req := httptest.NewRequest("POST", "/api/auth/mcp-consent", strings.NewReader(consentBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withAuthClaims(req.Context(), "test@example.com", "tenant-123", "test-tenant"))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "consent approve should succeed")

	var consentResp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&consentResp))
	consentRedirect := consentResp["redirect_url"]
	require.Contains(t, consentRedirect, "/oauth/callback", "should redirect to callback")
	require.Contains(t, consentRedirect, "code=", "should include consent code")
	require.Contains(t, consentRedirect, "state="+url.QueryEscape(mcpState), "should include state")

	// Step 4: GET /oauth/callback - exchange consent code for auth code.
	callbackResp, err := doGet(ctx, noRedirect, ts.URL+consentRedirect)
	require.NoError(t, err)
	defer callbackResp.Body.Close()
	require.Equal(t, http.StatusFound, callbackResp.StatusCode, "callback should redirect to MCP client")

	callbackLocation := callbackResp.Header.Get("Location")
	require.Contains(t, callbackLocation, "code=", "callback redirect should include auth code")
	require.Contains(t, callbackLocation, "state=mcp-client-state", "should forward MCP client state")

	// Extract the authorization code from the redirect.
	callbackRedirectURL, err := url.Parse(callbackLocation)
	require.NoError(t, err)
	authCode := callbackRedirectURL.Query().Get("code")
	require.NotEmpty(t, authCode)

	// Step 5: POST /oauth/token - exchange auth code + PKCE verifier for JWT.
	tokenResp, err := doPostForm(ctx, http.DefaultClient, ts.URL+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"code_verifier": {verifier},
		"client_id":     {"test-mcp-client"},
		"redirect_uri":  {"http://localhost:8090/oauth/callback"},
	})
	require.NoError(t, err)
	defer tokenResp.Body.Close()
	require.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var tokenBody map[string]interface{}
	require.NoError(t, json.NewDecoder(tokenResp.Body).Decode(&tokenBody))
	accessToken, ok := tokenBody["access_token"].(string)
	require.True(t, ok, "response should include access_token")
	require.NotEmpty(t, accessToken, "access_token should not be empty")
	assert.Equal(t, "Bearer", tokenBody["token_type"])
}

// TestMCPOAuthDenyFlow tests that denying consent returns an error redirect.
func TestMCPOAuthDenyFlow(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "localhost")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceChallenge(verifier)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := doGet(ctx, noRedirect, ts.URL+"/oauth/authorize?"+url.Values{
		"client_id":             {"test-mcp-client"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://localhost:8090/oauth/callback"},
		"state":                 {"mcp-deny-state"},
		"scope":                 {"mcp:default"},
	}.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()

	redirectURL, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	mcpState := redirectURL.Query().Get("mcp_state")

	// Deny consent.
	consentBody := `{"mcp_state":"` + mcpState + `","client_id":"test-mcp-client","action":"deny"}`
	req := httptest.NewRequest("POST", "/api/auth/mcp-consent", strings.NewReader(consentBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withAuthClaims(req.Context(), "test@example.com", "tenant-123", "test-tenant"))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var denyResp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&denyResp))
	denyRedirect := denyResp["redirect_url"]
	require.Contains(t, denyRedirect, "error=access_denied", "deny should return access_denied error")
	require.Contains(t, denyRedirect, "state=mcp-deny-state", "should include original MCP client state")
}

// TestMCPOAuthPKCEIntegrity ensures that a wrong PKCE verifier is rejected at token exchange.
func TestMCPOAuthPKCEIntegrity(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "localhost")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	verifier := "correct-verifier-for-pkce-test"
	challenge := pkceChallenge(verifier)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Authorize
	resp, err := doGet(ctx, noRedirect, ts.URL+"/oauth/authorize?"+url.Values{
		"client_id":             {"test-mcp-client"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://localhost:8090/oauth/callback"},
		"scope":                 {"mcp:default"},
	}.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()

	redirectURL, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	mcpState := redirectURL.Query().Get("mcp_state")

	// Approve consent
	consentBody := `{"mcp_state":"` + mcpState + `","client_id":"test-mcp-client","action":"approve"}`
	req := httptest.NewRequest("POST", "/api/auth/mcp-consent", strings.NewReader(consentBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withAuthClaims(req.Context(), "test@example.com", "tenant-123", "test-tenant"))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var consentResp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&consentResp))

	// Callback
	callbackResp, err := doGet(ctx, noRedirect, ts.URL+consentResp["redirect_url"])
	require.NoError(t, err)
	defer callbackResp.Body.Close()

	callbackURL, err := url.Parse(callbackResp.Header.Get("Location"))
	require.NoError(t, err)
	authCode := callbackURL.Query().Get("code")

	// Token exchange with WRONG verifier
	tokenResp, err := doPostForm(ctx, http.DefaultClient, ts.URL+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authCode},
		"code_verifier": {"wrong-verifier-should-fail"},
		"client_id":     {"test-mcp-client"},
	})
	require.NoError(t, err)
	defer tokenResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, tokenResp.StatusCode, "wrong PKCE verifier should fail")

	body, _ := io.ReadAll(tokenResp.Body)
	assert.Contains(t, string(body), "PKCE", "error should mention PKCE")
}

// TestMCPOAuthTenantIsolation verifies that cross-tenant consent is rejected.
func TestMCPOAuthTenantIsolation(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "example.com")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	verifier := "tenant-isolation-verifier"
	challenge := pkceChallenge(verifier)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Authorize - uses default tenant slug "test-tenant" since no subdomain.
	resp, err := doGet(ctx, noRedirect, ts.URL+"/oauth/authorize?"+url.Values{
		"client_id":             {"test-mcp-client"},
		"response_type":         {"code"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://localhost:8090/oauth/callback"},
		"scope":                 {"mcp:default"},
	}.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()

	redirectURL, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	mcpState := redirectURL.Query().Get("mcp_state")
	require.NotEmpty(t, mcpState)

	// Attempt consent with a DIFFERENT tenant slug.
	consentBody := `{"mcp_state":"` + mcpState + `","client_id":"test-mcp-client","action":"approve"}`
	req := httptest.NewRequest("POST", "/api/auth/mcp-consent", strings.NewReader(consentBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withAuthClaims(req.Context(), "attacker@example.com", "different-tenant-id", "different-tenant"))

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, "cross-tenant consent should be rejected")

	var errResp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	assert.Equal(t, "tenant_mismatch", errResp["error"])
}

// TestMCPOAuthMetadataEndpoint verifies the OAuth authorization server metadata.
func TestMCPOAuthMetadataEndpoint(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "localhost")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := doGet(ctx, http.DefaultClient, ts.URL+"/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var meta map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&meta))
	assert.Contains(t, meta["authorization_endpoint"], "/oauth/authorize")
	assert.Contains(t, meta["token_endpoint"], "/oauth/token")
	assert.Contains(t, meta["registration_endpoint"], "/oauth/register")
}

// TestMCPOAuthDynamicClientRegistration verifies dynamic client registration works.
func TestMCPOAuthDynamicClientRegistration(t *testing.T) {
	ctx := context.Background()
	signer := testSigner(t)
	srv, cleanup := buildTestGateway(t, signer, "localhost")
	defer cleanup()

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	regBody := `{
		"client_name": "Test MCP Client",
		"redirect_uris": ["https://example.com/callback"],
		"grant_types": ["authorization_code"],
		"response_types": ["code"],
		"token_endpoint_auth_method": "none"
	}`
	resp, err := doPostJSON(ctx, http.DefaultClient, ts.URL+"/oauth/register", regBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var regResp map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&regResp))
	assert.NotEmpty(t, regResp["client_id"])
	assert.Equal(t, "Test MCP Client", regResp["client_name"])
}
