package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestSigner creates a JWTSigner with an auto-generated dev key.
func newTestSigner(t *testing.T) *platformauth.JWTSigner {
	t.Helper()
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{})
	require.NoError(t, err)
	return signer
}

// newTestOIDCStateStore creates an OIDCStateStore and registers cleanup with t.
func newTestOIDCStateStore(t *testing.T) *auth.OIDCStateStore {
	t.Helper()
	s := auth.NewOIDCStateStore()
	t.Cleanup(s.Close)
	return s
}

// fakeDexServer creates a test HTTP server that simulates Dex's token endpoint.
// It returns a server that responds with a fake ID token containing the given email.
func fakeDexServer(t *testing.T, email string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Simulate Dex authorization endpoint — redirect immediately with a code
	mux.HandleFunc("/dex/auth", func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		target, _ := url.Parse(redirectURI)
		q := target.Query()
		q.Set("code", "fake-dex-code")
		q.Set("state", state)
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	})

	// Simulate Dex token endpoint — return a fake ID token.
	// Asserts required PKCE exchange fields to catch regressions in exchangeDexCode.
	mux.HandleFunc("/dex/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		// Assert required token exchange parameters.
		for _, field := range []string{"grant_type", "code", "redirect_uri", "code_verifier", "client_id"} {
			if r.PostForm.Get(field) == "" {
				http.Error(w, "missing required field: "+field, http.StatusBadRequest)
				return
			}
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			http.Error(w, "wrong grant_type", http.StatusBadRequest)
			return
		}

		// Build a fake JWT with just the email claim (no signature validation needed).
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"` + email + `","sub":"fake-sub"}`))
		fakeJWT := header + "." + payload + ".fake-signature"

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token":     fakeJWT,
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// -----------------------------------------------------------------------
// OIDCStateStore
// -----------------------------------------------------------------------

func TestOIDCStateStore_StoreAndConsume(t *testing.T) {
	store := newTestOIDCStateStore(t)

	entry := auth.OIDCFlowState{
		MCPCodeChallenge: "test-challenge",
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		MCPState:         "client-state",
		DexCodeVerifier:  "dex-verifier",
		TenantSlug:       "acme",
		IssuedAt:         time.Now(),
	}

	key, err := store.Store(entry)
	require.NoError(t, err)
	require.NotEmpty(t, key)

	consumed, ok := store.Consume(key)
	require.True(t, ok)
	assert.Equal(t, "test-challenge", consumed.MCPCodeChallenge)
	assert.Equal(t, "meridian-mcp", consumed.MCPClientID)
	assert.Equal(t, "acme", consumed.TenantSlug)

	// Second consume should fail (one-time use)
	_, ok2 := store.Consume(key)
	assert.False(t, ok2)
}

func TestOIDCStateStore_ExpiredEntry(t *testing.T) {
	store := newTestOIDCStateStore(t)

	entry := auth.OIDCFlowState{
		MCPCodeChallenge: "expired-challenge",
		MCPClientID:      "meridian-mcp",
		IssuedAt:         time.Now().Add(-11 * time.Minute), // expired
	}

	key, err := store.Store(entry)
	require.NoError(t, err)

	_, ok := store.Consume(key)
	assert.False(t, ok, "expired entry must not be consumable")
}

func TestOIDCStateStore_UnknownKey(t *testing.T) {
	store := newTestOIDCStateStore(t)

	_, ok := store.Consume("nonexistent-key")
	assert.False(t, ok)
}

// -----------------------------------------------------------------------
// OIDCHandler — HandleAuthorize
// -----------------------------------------------------------------------

func TestOIDCHandler_Authorize_RedirectsToDex(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	oauthCfg := auth.OAuthConfig{
		ClientID:         "meridian-mcp",
		RedirectURI:      "https://claude.ai/callback",
		AuthorizationURL: "https://demo.meridianhub.cloud/oauth/authorize",
		TokenURL:         "https://demo.meridianhub.cloud/oauth/token",
	}

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth:      oauthCfg,
		StateStore: stateStore,
		CodeStore:  codeStore,
		Signer:     signer,
		BaseDomain: "demo.meridianhub.cloud",
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"client-state-123"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	require.NotEmpty(t, location)

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Should redirect to Dex auth endpoint
	assert.Contains(t, redirectURL.Path, "/dex/auth")
	assert.Equal(t, "meridian-service", redirectURL.Query().Get("client_id"))
	assert.Equal(t, "code", redirectURL.Query().Get("response_type"))
	assert.Equal(t, "S256", redirectURL.Query().Get("code_challenge_method"))
	assert.NotEmpty(t, redirectURL.Query().Get("state"))
	assert.NotEmpty(t, redirectURL.Query().Get("code_challenge"))
}

func TestOIDCHandler_Authorize_InvalidClientID(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"wrong-client"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOIDCHandler_Authorize_MissingPKCE(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type": {"code"},
		"client_id":     {"meridian-mcp"},
		"redirect_uri":  {"https://claude.ai/callback"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOIDCHandler_Authorize_RejectsHTTPRedirect(t *testing.T) {
	registry := newTestRegistry(t)
	signer := newTestSigner(t)
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: "https://dex.example.com/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth:      auth.OAuthConfig{ClientID: "meridian-mcp"},
		Registry:   registry,
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	// Register a dynamic client with an HTTP redirect to test scheme validation.
	client, err := registry.Register(auth.RegisteredClient{
		RedirectURIs: []string{"http://evil.example.com/steal"},
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {"http://evil.example.com/steal"},
		"code_challenge":        {"abc123"},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "redirect_uri must use https")
}

func TestOIDCHandler_Authorize_AllowsLocalhostHTTP(t *testing.T) {
	dexSrv := fakeDexServer(t, "admin@acme.com")
	signer := newTestSigner(t)
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "http://localhost:3000/callback",
		},
		DefaultTenantSlug: "acme",
		StateStore:        newTestOIDCStateStore(t),
		CodeStore:         newTestStore(t),
		Signer:            signer,
		Logger:            slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"http://localhost:3000/callback"},
		"code_challenge":        {"abc123"},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	// Should redirect to Dex (302), not reject
	assert.Equal(t, http.StatusFound, w.Code)
}

// -----------------------------------------------------------------------
// OIDCHandler — HandleCallback
// -----------------------------------------------------------------------

func TestOIDCHandler_Callback_ExchangesAndRedirects(t *testing.T) {
	dexSrv := fakeDexServer(t, "admin@acme.com")
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		StateStore: stateStore,
		CodeStore:  codeStore,
		Signer:     signer,
		BaseDomain: "demo.meridianhub.cloud",
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	// Pre-store OIDC flow state (simulates what HandleAuthorize would do)
	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		MCPState:         "client-state-456",
		DexCodeVerifier:  "test-verifier",
		TenantSlug:       "acme",
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	// Simulate Dex callback
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	require.NotEmpty(t, location)

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Should redirect to Claude's callback with auth code
	assert.Equal(t, "claude.ai", redirectURL.Host)
	assert.Equal(t, "/callback", redirectURL.Path)
	assert.NotEmpty(t, redirectURL.Query().Get("code"), "must include authorization code")
	assert.Equal(t, "client-state-456", redirectURL.Query().Get("state"))

	// The auth code should be consumable from the code store
	mcpCode := redirectURL.Query().Get("code")
	entry, ok := codeStore.Consume(mcpCode)
	require.True(t, ok)
	assert.Equal(t, challenge, entry.CodeChallenge)
	assert.Equal(t, "meridian-mcp", entry.ClientID)
	assert.NotEmpty(t, entry.Token, "must have pre-signed JWT")

	// Validate the JWT
	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	claims, err := validator.ValidateToken(entry.Token)
	require.NoError(t, err)
	assert.Equal(t, "admin@acme.com", claims.Email)
	assert.Equal(t, "acme", claims.TenantID)
}

func TestOIDCHandler_Callback_InvalidState(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {"invalid-state-key"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOIDCHandler_Callback_DexError(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"error":             {"access_denied"},
		"error_description": {"user cancelled"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// -----------------------------------------------------------------------
// End-to-end: Authorize → Callback → Token Exchange
// -----------------------------------------------------------------------

func TestOIDCFlow_EndToEnd(t *testing.T) {
	dexSrv := fakeDexServer(t, "operator@acme.com")
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	oauthCfg := auth.OAuthConfig{
		ClientID:         "meridian-mcp",
		RedirectURI:      "https://claude.ai/callback",
		AuthorizationURL: "https://demo.meridianhub.cloud/oauth/authorize",
		TokenURL:         "https://demo.meridianhub.cloud/oauth/token",
	}

	oidcHandler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth:      oauthCfg,
		StateStore: stateStore,
		CodeStore:  codeStore,
		Signer:     signer,
		BaseDomain: "demo.meridianhub.cloud",
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	tokenHandler := auth.NewTokenHandler(oauthCfg, codeStore, &fakeTokenIssuer{token: "fallback"})

	verifier, challenge := generatePKCEPair(t)

	// Step 1: Authorize — get redirect to Dex
	authReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"e2e-state"},
	}.Encode(), nil)
	authReq.Host = "acme.demo.meridianhub.cloud"
	authW := httptest.NewRecorder()
	oidcHandler.HandleAuthorize(authW, authReq)
	require.Equal(t, http.StatusFound, authW.Code)

	// Extract state from Dex redirect
	dexRedirect, err := url.Parse(authW.Header().Get("Location"))
	require.NoError(t, err)
	internalState := dexRedirect.Query().Get("state")
	require.NotEmpty(t, internalState)

	// Step 2: Callback — simulate Dex returning with code
	cbReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {internalState},
	}.Encode(), nil)
	cbW := httptest.NewRecorder()
	oidcHandler.HandleCallback(cbW, cbReq)
	require.Equal(t, http.StatusFound, cbW.Code)

	// Extract auth code from redirect to Claude
	claudeRedirect, err := url.Parse(cbW.Header().Get("Location"))
	require.NoError(t, err)
	mcpCode := claudeRedirect.Query().Get("code")
	require.NotEmpty(t, mcpCode)
	assert.Equal(t, "e2e-state", claudeRedirect.Query().Get("state"))

	// Step 3: Token exchange — exchange code for JWT
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {mcpCode},
		"client_id":     {"meridian-mcp"},
		"code_verifier": {verifier},
	}
	tokenReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenW := httptest.NewRecorder()
	tokenHandler.ServeHTTP(tokenW, tokenReq)
	require.Equal(t, http.StatusOK, tokenW.Code)

	var tokenResp map[string]any
	require.NoError(t, json.Unmarshal(tokenW.Body.Bytes(), &tokenResp))
	assert.Equal(t, "Bearer", tokenResp["token_type"])

	accessToken, ok := tokenResp["access_token"].(string)
	require.True(t, ok)
	assert.NotEqual(t, "fallback", accessToken, "must use pre-signed JWT, not fallback issuer")

	// Validate the JWT
	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	claims, err := validator.ValidateToken(accessToken)
	require.NoError(t, err)
	assert.Equal(t, "operator@acme.com", claims.Email)
	assert.Equal(t, "acme", claims.TenantID)
}

// -----------------------------------------------------------------------
// CodeStore with pre-signed token
// -----------------------------------------------------------------------

func TestCodeStore_StoreWithToken(t *testing.T) {
	store := newTestStore(t)

	_, challenge := generatePKCEPair(t)
	store.StoreWithToken("code-with-token", auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      "meridian-mcp",
		RedirectURI:   "https://claude.ai/callback",
		IssuedAt:      time.Now(),
	}, "pre-signed-jwt-token")

	entry, ok := store.Consume("code-with-token")
	require.True(t, ok)
	assert.Equal(t, "pre-signed-jwt-token", entry.Token)
	assert.Equal(t, challenge, entry.CodeChallenge)
}

func TestCodeStore_StoreWithoutToken_HasEmptyToken(t *testing.T) {
	store := newTestStore(t)

	_, challenge := generatePKCEPair(t)
	store.Store("code-no-token", auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      "meridian-mcp",
		RedirectURI:   "https://claude.ai/callback",
		IssuedAt:      time.Now(),
	})

	entry, ok := store.Consume("code-no-token")
	require.True(t, ok)
	assert.Empty(t, entry.Token)
}

func TestTokenHandler_UsesPreSignedToken(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	preSignedJWT := "eyJhbGciOiJSUzI1NiJ9.eyJlbWFpbCI6InVzZXJAZXhhbXBsZS5jb20ifQ.signature"

	store.StoreWithToken("code-presigned", auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now(),
	}, preSignedJWT)

	// The issuer should NOT be called when a pre-signed token exists
	issuer := &fakeTokenIssuer{token: "should-not-be-used"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"code-presigned"},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, preSignedJWT, resp["access_token"], "must return pre-signed JWT, not issuer token")
}

// -----------------------------------------------------------------------
// OIDCHandler — validation
// -----------------------------------------------------------------------

func TestNewOIDCHandler_MissingConfig(t *testing.T) {
	signer := newTestSigner(t)

	tests := []struct {
		name string
		cfg  auth.OIDCHandlerConfig
	}{
		{
			name: "missing dex issuer",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{ClientID: "c", CallbackURL: "https://x/cb"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing client ID",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{DexIssuerURL: "https://dex", CallbackURL: "https://x/cb"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing callback URL",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{DexIssuerURL: "https://dex", ClientID: "c"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing state store",
			cfg: auth.OIDCHandlerConfig{
				OIDC:      auth.OIDCConfig{DexIssuerURL: "https://dex", ClientID: "c", CallbackURL: "https://x/cb"},
				CodeStore: newTestStore(t),
				Signer:    signer,
				Logger:    slog.Default(),
			},
		},
		{
			name: "missing code store",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{DexIssuerURL: "https://dex", ClientID: "c", CallbackURL: "https://x/cb"},
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing signer",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{DexIssuerURL: "https://dex", ClientID: "c", CallbackURL: "https://x/cb"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing logger",
			cfg: auth.OIDCHandlerConfig{
				OIDC:       auth.OIDCConfig{DexIssuerURL: "https://dex", ClientID: "c", CallbackURL: "https://x/cb"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.NewOIDCHandler(tt.cfg)
			assert.Error(t, err)
		})
	}
}

func TestOIDCHandler_Authorize_UsesExternalDexURL(t *testing.T) {
	// When BaseURL is set and DexIssuerURL points to an internal Docker hostname,
	// the browser redirect should use the external URL, not the internal one.
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex", // Internal: http://127.0.0.1:PORT/dex
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		BaseURL:    "https://demo.meridianhub.cloud",
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		BaseDomain: "demo.meridianhub.cloud",
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"client-state-123"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// The redirect should go to the external host with tenant subdomain, not the internal test server.
	assert.Equal(t, "acme.demo.meridianhub.cloud", redirectURL.Host)
	assert.Equal(t, "https", redirectURL.Scheme)
	assert.Equal(t, "/dex/auth", redirectURL.Path)
}

func TestOIDCHandler_Authorize_RejectsStaticClientEvilRedirect(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@example.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://evil.example.com/cb"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "redirect_uri does not match registered value")
}

// -----------------------------------------------------------------------
// Task 3: Tenant-scoped Dex redirect in HandleAuthorize
// -----------------------------------------------------------------------

func TestHandleAuthorize_WithTenantSubdomain(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@acme.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		BaseURL:    "https://demo.meridianhub.cloud",
		BaseDomain: "demo.meridianhub.cloud",
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"test-state"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Dex redirect should use tenant-scoped URL
	assert.Equal(t, "acme.demo.meridianhub.cloud", redirectURL.Host)
	assert.Equal(t, "https", redirectURL.Scheme)
	assert.Equal(t, "/dex/auth", redirectURL.Path)
}

func TestHandleAuthorize_WithDefaultTenant(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@acme.com")
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		DefaultTenantSlug: "volterra",
		BaseDomain:        "demo.meridianhub.cloud",
		StateStore:        newTestOIDCStateStore(t),
		CodeStore:         newTestStore(t),
		Signer:            signer,
		Logger:            slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	// Request on bare domain (no subdomain) — should use default tenant
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"test-state"},
	}.Encode(), nil)
	req.Host = "demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode, "bare domain with default tenant should succeed")
}

func TestHandleAuthorize_MultiTenantNoSubdomain_FailsClosed(t *testing.T) {
	dexSrv := fakeDexServer(t, "user@acme.com")
	signer := newTestSigner(t)

	// No DefaultTenantSlug — multi-tenant mode
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		BaseDomain: "demo.meridianhub.cloud",
		StateStore: newTestOIDCStateStore(t),
		CodeStore:  newTestStore(t),
		Signer:     signer,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	// Request on bare domain with no default — should fail closed
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	req.Host = "demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "tenant identification required")
}

func TestBuildTenantScopedDexURL(t *testing.T) {
	tests := []struct {
		name       string
		dexBaseURL string
		tenant     string
		baseDomain string
		want       string
	}{
		{
			name:       "scopes URL with tenant subdomain",
			dexBaseURL: "https://demo.meridianhub.cloud/dex",
			tenant:     "acme",
			baseDomain: "demo.meridianhub.cloud",
			want:       "https://acme.demo.meridianhub.cloud/dex",
		},
		{
			name:       "empty base domain returns original",
			dexBaseURL: "https://demo.meridianhub.cloud/dex",
			tenant:     "acme",
			baseDomain: "",
			want:       "https://demo.meridianhub.cloud/dex",
		},
		{
			name:       "empty tenant returns original",
			dexBaseURL: "https://demo.meridianhub.cloud/dex",
			tenant:     "",
			baseDomain: "demo.meridianhub.cloud",
			want:       "https://demo.meridianhub.cloud/dex",
		},
		{
			name:       "preserves port in URL",
			dexBaseURL: "https://demo.meridianhub.cloud:8443/dex",
			tenant:     "acme",
			baseDomain: "demo.meridianhub.cloud",
			want:       "https://acme.demo.meridianhub.cloud:8443/dex",
		},
		{
			name:       "non-matching host returns original",
			dexBaseURL: "http://dex:5556/dex",
			tenant:     "acme",
			baseDomain: "demo.meridianhub.cloud",
			want:       "http://dex:5556/dex",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auth.BuildTenantScopedDexURL(tt.dexBaseURL, tt.tenant, tt.baseDomain)
			assert.Equal(t, tt.want, got)
		})
	}
}

// -----------------------------------------------------------------------
// Task 5: Tenant slug to UUID resolution in HandleCallback
// -----------------------------------------------------------------------

// mockTenantResolver is a test double for TenantSlugResolver.
type mockTenantResolver struct {
	mapping map[string]string
	err     error
}

func (m *mockTenantResolver) ResolveSlug(_ context.Context, slug string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	uuid, ok := m.mapping[slug]
	if !ok {
		return "", fmt.Errorf("tenant not found: %s", slug)
	}
	return uuid, nil
}

func TestHandleCallback_ResolvesSlugToUUID(t *testing.T) {
	dexSrv := fakeDexServer(t, "admin@acme.com")
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	resolver := &mockTenantResolver{
		mapping: map[string]string{"acme": "550e8400-e29b-41d4-a716-446655440000"},
	}

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		TenantResolver: resolver,
		StateStore:     stateStore,
		CodeStore:      codeStore,
		Signer:         signer,
		BaseDomain:     "demo.meridianhub.cloud",
		Logger:         slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		DexCodeVerifier:  "test-verifier",
		TenantSlug:       "acme",
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	// Extract the MCP code and verify the JWT has UUID, not slug
	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	mcpCode := redirectURL.Query().Get("code")
	entry, ok := codeStore.Consume(mcpCode)
	require.True(t, ok)

	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	claims, err := validator.ValidateToken(entry.Token)
	require.NoError(t, err)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", claims.TenantID, "JWT must contain UUID, not slug")
	assert.Equal(t, "admin@acme.com", claims.Email)
}

func TestHandleCallback_ResolverFailure_ReturnsError(t *testing.T) {
	dexSrv := fakeDexServer(t, "admin@acme.com")
	signer := newTestSigner(t)
	stateStore := newTestOIDCStateStore(t)

	resolver := &mockTenantResolver{
		err: fmt.Errorf("database connection failed"),
	}

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		TenantResolver: resolver,
		StateStore:     stateStore,
		CodeStore:      newTestStore(t),
		Signer:         signer,
		Logger:         slog.Default(),
	})
	require.NoError(t, err)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: "test-challenge",
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		DexCodeVerifier:  "test-verifier",
		TenantSlug:       "acme",
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "tenant slug resolution failed")
}

func TestHandleCallback_NoResolver_FallsBackToSlug(t *testing.T) {
	dexSrv := fakeDexServer(t, "admin@acme.com")
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	// No TenantResolver set — slug should be used as-is
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OIDC: auth.OIDCConfig{
			DexIssuerURL: dexSrv.URL + "/dex",
			ClientID:     "meridian-service",
			CallbackURL:  "https://demo.meridianhub.cloud/oauth/callback",
		},
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		StateStore: stateStore,
		CodeStore:  codeStore,
		Signer:     signer,
		BaseDomain: "demo.meridianhub.cloud",
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	_, challenge := generatePKCEPair(t)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		DexCodeVerifier:  "test-verifier",
		TenantSlug:       "acme",
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"fake-dex-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	mcpCode := redirectURL.Query().Get("code")
	entry, ok := codeStore.Consume(mcpCode)
	require.True(t, ok)

	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)
	claims, err := validator.ValidateToken(entry.Token)
	require.NoError(t, err)

	assert.Equal(t, "acme", claims.TenantID, "without resolver, JWT should contain raw slug")
}
