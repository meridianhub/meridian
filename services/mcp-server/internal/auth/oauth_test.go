package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generatePKCEPair produces a code_verifier and code_challenge for tests.
func generatePKCEPair(t *testing.T) (verifier, challenge string) {
	t.Helper()
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" // 43-char base64url per RFC 7636
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// newTestStore creates a CodeStore and registers cleanup with t.
func newTestStore(t *testing.T) *auth.CodeStore {
	t.Helper()
	s := auth.NewCodeStore()
	t.Cleanup(s.Close)
	return s
}

// -----------------------------------------------------------------------
// OAuthConfig
// -----------------------------------------------------------------------

func TestOAuthConfig_Defaults(t *testing.T) {
	cfg := auth.OAuthConfig{
		ClientID:         "meridian-mcp",
		AuthorizationURL: "https://auth.example.com/authorize",
		TokenURL:         "https://auth.example.com/token",
		RedirectURI:      "http://localhost:8090/oauth/callback",
	}
	assert.Equal(t, "meridian-mcp", cfg.ClientID)
	assert.Equal(t, "https://auth.example.com/authorize", cfg.AuthorizationURL)
	assert.Equal(t, "https://auth.example.com/token", cfg.TokenURL)
}

// -----------------------------------------------------------------------
// AuthMetadata (401 response body)
// -----------------------------------------------------------------------

func TestAuthMetadata_JSON(t *testing.T) {
	meta := auth.Metadata{
		AuthorizationURL: "https://auth.example.com/authorize",
		TokenURL:         "https://auth.example.com/token",
	}

	data, err := json.Marshal(meta)
	require.NoError(t, err)

	var decoded map[string]string
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "https://auth.example.com/authorize", decoded["authorization_url"])
	assert.Equal(t, "https://auth.example.com/token", decoded["token_url"])
}

// -----------------------------------------------------------------------
// MetadataHandler — /.well-known/oauth-authorization-server
// -----------------------------------------------------------------------

func TestMetadataHandler_ServesRFC8414(t *testing.T) {
	handler := auth.NewMetadataHandler("https://mcp.example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "mcp.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "public")

	var meta auth.AuthorizationServerMetadata
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))

	assert.Equal(t, "https://mcp.example.com", meta.Issuer)
	assert.Equal(t, "https://mcp.example.com/oauth/authorize", meta.AuthorizationEndpoint)
	assert.Equal(t, "https://mcp.example.com/oauth/token", meta.TokenEndpoint)
	assert.Equal(t, []string{"code"}, meta.ResponseTypesSupported)
	assert.Equal(t, []string{"authorization_code"}, meta.GrantTypesSupported)
	assert.Equal(t, []string{"S256"}, meta.CodeChallengeMethodsSupported)
	assert.Equal(t, []string{"none"}, meta.TokenEndpointAuthMethodsSupported)
}

func TestMetadataHandler_DerivesTenantScopedURLs(t *testing.T) {
	handler := auth.NewMetadataHandler("https://demo.meridianhub.cloud")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "volterra-energy.demo.meridianhub.cloud"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var meta auth.AuthorizationServerMetadata
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))

	assert.Equal(t, "https://volterra-energy.demo.meridianhub.cloud", meta.Issuer)
	assert.Equal(t, "https://volterra-energy.demo.meridianhub.cloud/oauth/authorize", meta.AuthorizationEndpoint)
	assert.Equal(t, "https://volterra-energy.demo.meridianhub.cloud/oauth/token", meta.TokenEndpoint)
}

func TestMetadataHandler_UsesFallbackWhenNoHost(t *testing.T) {
	handler := auth.NewMetadataHandler("https://fallback.example.com")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "" // Explicitly clear the default Host set by httptest
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var meta auth.AuthorizationServerMetadata
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))

	assert.Equal(t, "https://fallback.example.com", meta.Issuer)
}

// -----------------------------------------------------------------------
// AuthorizationHandler
// -----------------------------------------------------------------------

func TestAuthorizationHandler_GeneratesCode(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:         "meridian-mcp",
		AuthorizationURL: "https://auth.example.com/authorize",
		TokenURL:         "https://auth.example.com/token",
		RedirectURI:      "http://localhost:8090/oauth/callback",
	}
	handler := auth.NewAuthorizationHandler(cfg, store, nil)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {cfg.RedirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"random-state"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should redirect to redirect_uri with code
	resp := w.Result()
	assert.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	require.NotEmpty(t, location)

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	code := redirectURL.Query().Get("code")
	assert.NotEmpty(t, code, "redirect must include authorization code")
	assert.Equal(t, "random-state", redirectURL.Query().Get("state"))
}

func TestAuthorizationHandler_MissingChallenge_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}
	handler := auth.NewAuthorizationHandler(cfg, store, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?response_type=code&client_id=meridian-mcp", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorizationHandler_WrongClientID_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}
	handler := auth.NewAuthorizationHandler(cfg, store, nil)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"wrong-client"},
		"redirect_uri":          {cfg.RedirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorizationHandler_WrongRedirectURI_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}
	handler := auth.NewAuthorizationHandler(cfg, store, nil)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {"https://evil.example.com/steal"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorizationHandler_NonGetMethod_ReturnsMethodNotAllowed(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}
	handler := auth.NewAuthorizationHandler(cfg, store, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/authorize", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// -----------------------------------------------------------------------
// TokenHandler
// -----------------------------------------------------------------------

func TestTokenHandler_ExchangesCodeForToken(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	verifier, challenge := generatePKCEPair(t)

	// Pre-store a valid auth code
	code := "test-auth-code-1234"
	store.Store(code, auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now(),
	})

	// Provide a token issuer that returns a fake JWT
	issuer := &fakeTokenIssuer{token: "eyJhbGci.eyJzdWIi.sig"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
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
	assert.Equal(t, "Bearer", resp["token_type"])
	assert.NotEmpty(t, resp["access_token"])
}

func TestTokenHandler_InvalidVerifier_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	_, challenge := generatePKCEPair(t)

	code := "test-auth-code-pkce-fail"
	store.Store(code, auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now(),
	})

	issuer := &fakeTokenIssuer{token: "some-token"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {"wrong-verifier-AAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, // 43+ chars per RFC 7636
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTokenHandler_ExpiredCode_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	verifier, challenge := generatePKCEPair(t)

	// Store code that expired 11 minutes ago
	code := "expired-auth-code"
	store.Store(code, auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now().Add(-11 * time.Minute),
	})

	issuer := &fakeTokenIssuer{token: "some-token"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTokenHandler_UnknownCode_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	issuer := &fakeTokenIssuer{token: "some-token"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	verifier, _ := generatePKCEPair(t)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"no-such-code"},
		"redirect_uri":  {cfg.RedirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTokenHandler_CodeIsConsumedAfterExchange(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	verifier, challenge := generatePKCEPair(t)
	code := "one-time-code"
	store.Store(code, auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now(),
	})

	issuer := &fakeTokenIssuer{token: "tok"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	makeRequest := func() *httptest.ResponseRecorder {
		form := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {cfg.RedirectURI},
			"client_id":     {cfg.ClientID},
			"code_verifier": {verifier},
		}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// First exchange: must succeed
	w1 := makeRequest()
	require.Equal(t, http.StatusOK, w1.Code)

	// Second exchange: code already consumed
	w2 := makeRequest()
	assert.Equal(t, http.StatusBadRequest, w2.Code)
}

func TestTokenHandler_MismatchedRedirectURI_ReturnsBadRequest(t *testing.T) {
	store := newTestStore(t)
	cfg := auth.OAuthConfig{
		ClientID:    "meridian-mcp",
		RedirectURI: "http://localhost:8090/oauth/callback",
	}

	verifier, challenge := generatePKCEPair(t)
	code := "code-redirect-mismatch"
	store.Store(code, auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      cfg.ClientID,
		RedirectURI:   cfg.RedirectURI,
		IssuedAt:      time.Now(),
	})

	issuer := &fakeTokenIssuer{token: "tok"}
	handler := auth.NewTokenHandler(cfg, store, issuer)

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://evil.example.com/steal"},
		"client_id":     {cfg.ClientID},
		"code_verifier": {verifier},
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/token",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// -----------------------------------------------------------------------
// AuthCodeStore
// -----------------------------------------------------------------------

func TestAuthCodeStore_TTL(t *testing.T) {
	store := newTestStore(t)

	_, challenge := generatePKCEPair(t)
	entry := auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      "c",
		RedirectURI:   "r",
		IssuedAt:      time.Now().Add(-11 * time.Minute), // already expired
	}
	store.Store("expired", entry)

	_, ok := store.Consume("expired")
	assert.False(t, ok, "expired code must not be consumable")
}

func TestAuthCodeStore_ConsumeOnlyOnce(t *testing.T) {
	store := newTestStore(t)

	_, challenge := generatePKCEPair(t)
	entry := auth.CodeEntry{
		CodeChallenge: challenge,
		ClientID:      "c",
		RedirectURI:   "r",
		IssuedAt:      time.Now(),
	}
	store.Store("mycode", entry)

	e, ok := store.Consume("mycode")
	require.True(t, ok)
	assert.Equal(t, challenge, e.CodeChallenge)

	_, ok2 := store.Consume("mycode")
	assert.False(t, ok2)
}

// -----------------------------------------------------------------------
// Bearer middleware
// -----------------------------------------------------------------------

func TestBearerMiddleware_RejectsUnauthenticated(t *testing.T) {
	validator := &alwaysFailValidator{}
	mw := auth.NewBearerMiddleware(validator, "https://auth.example.com")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/mcp", nil)
	req.Host = "auth.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	mw.Handler(inner).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "https://auth.example.com/oauth/authorize", body["authorization_url"])
	assert.Equal(t, "https://auth.example.com/oauth/token", body["token_url"])
}

func TestBearerMiddleware_RejectsUnauthenticated_TenantScoped(t *testing.T) {
	validator := &alwaysFailValidator{}
	mw := auth.NewBearerMiddleware(validator, "https://demo.meridianhub.cloud")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/mcp", nil)
	req.Host = "volterra-energy.demo.meridianhub.cloud"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	mw.Handler(inner).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "https://volterra-energy.demo.meridianhub.cloud/oauth/authorize", body["authorization_url"])
	assert.Equal(t, "https://volterra-energy.demo.meridianhub.cloud/oauth/token", body["token_url"])
}

func TestBearerMiddleware_AcceptsValidToken(t *testing.T) {
	validator := &alwaysOKValidator{}
	mw := auth.NewBearerMiddleware(validator, "https://auth.example.com")

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()
	mw.Handler(inner).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "inner handler must be called for valid token")
}

// -----------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------

type fakeTokenIssuer struct {
	token string
}

func (f *fakeTokenIssuer) Issue(_ map[string]any) (string, error) {
	return f.token, nil
}

type alwaysFailValidator struct{}

func (v *alwaysFailValidator) ValidateBearer(_ string) error {
	return auth.ErrInvalidBearerToken
}

type alwaysOKValidator struct{}

func (v *alwaysOKValidator) ValidateBearer(_ string) error {
	return nil
}
