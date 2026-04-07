package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

func TestOIDCHandler_Authorize_InvalidClientID(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		ConsentStore: newFakeConsentStore(),
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
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
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore: newFakeConsentStore(),
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
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
		OAuth:        auth.OAuthConfig{ClientID: "meridian-mcp"},
		ConsentStore: newFakeConsentStore(),
		Registry:     registry,
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
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
	signer := newTestSigner(t)
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "http://localhost:3000/callback",
		},
		ConsentStore:      newFakeConsentStore(),
		DefaultTenantSlug: "acme",
		BaseURL:           "https://demo.meridianhub.cloud",
		BaseDomain:        "demo.meridianhub.cloud",
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

	// Should redirect to consent page (302), not reject
	assert.Equal(t, http.StatusFound, w.Code)
}

// -----------------------------------------------------------------------
// OIDCHandler — HandleCallback
// -----------------------------------------------------------------------

func TestOIDCHandler_Callback_InvalidState(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID: "meridian-mcp",
		},
		ConsentStore: newFakeConsentStore(),
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"some-consent-code"},
		"state": {"invalid-state-key"},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
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
			name: "missing state store",
			cfg: auth.OIDCHandlerConfig{
				OAuth:        auth.OAuthConfig{ClientID: "c"},
				ConsentStore: newFakeConsentStore(),
				CodeStore:    newTestStore(t),
				Signer:       signer,
				Logger:       slog.Default(),
			},
		},
		{
			name: "missing code store",
			cfg: auth.OIDCHandlerConfig{
				OAuth:        auth.OAuthConfig{ClientID: "c"},
				ConsentStore: newFakeConsentStore(),
				StateStore:   newTestOIDCStateStore(t),
				Signer:       signer,
				Logger:       slog.Default(),
			},
		},
		{
			name: "missing consent store",
			cfg: auth.OIDCHandlerConfig{
				OAuth:      auth.OAuthConfig{ClientID: "c"},
				CodeStore:  newTestStore(t),
				StateStore: newTestOIDCStateStore(t),
				Signer:     signer,
				Logger:     slog.Default(),
			},
		},
		{
			name: "missing signer",
			cfg: auth.OIDCHandlerConfig{
				OAuth:        auth.OAuthConfig{ClientID: "c"},
				ConsentStore: newFakeConsentStore(),
				CodeStore:    newTestStore(t),
				StateStore:   newTestOIDCStateStore(t),
				Logger:       slog.Default(),
			},
		},
		{
			name: "missing logger",
			cfg: auth.OIDCHandlerConfig{
				OAuth:        auth.OAuthConfig{ClientID: "c"},
				ConsentStore: newFakeConsentStore(),
				CodeStore:    newTestStore(t),
				StateStore:   newTestOIDCStateStore(t),
				Signer:       signer,
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

func TestOIDCHandler_Authorize_RejectsStaticClientEvilRedirect(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore: newFakeConsentStore(),
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
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

func TestHandleAuthorize_WithDefaultTenant(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore:      newFakeConsentStore(),
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
	signer := newTestSigner(t)

	// No DefaultTenantSlug — multi-tenant mode
	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore: newFakeConsentStore(),
		BaseDomain:   "demo.meridianhub.cloud",
		StateStore:   newTestOIDCStateStore(t),
		CodeStore:    newTestStore(t),
		Signer:       signer,
		Logger:       slog.Default(),
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

func TestOIDCStateStore_PeekInfo(t *testing.T) {
	s := newTestOIDCStateStore(t)

	key, err := s.Store(auth.OIDCFlowState{
		MCPClientID:     "client-1",
		MCPRedirectURI:  "https://example.com/callback",
		RequestedScopes: []string{"mcp:default", "mcp:admin"},
		IssuedAt:        time.Now(),
	})
	require.NoError(t, err)

	result, ok := s.PeekInfo(key)
	assert.True(t, ok)
	assert.Equal(t, "client-1", result.ClientID)
	assert.Equal(t, "https://example.com/callback", result.RedirectURI)
	assert.Equal(t, []string{"mcp:default", "mcp:admin"}, result.Scopes)

	// PeekInfo is non-consuming - a second call should also succeed.
	_, ok = s.PeekInfo(key)
	assert.True(t, ok, "PeekInfo should not consume the entry")

	// Consume should still work after PeekInfo.
	entry, ok := s.Consume(key)
	assert.True(t, ok)
	assert.Equal(t, "client-1", entry.MCPClientID)
}

func TestOIDCStateStore_PeekInfo_Expired(t *testing.T) {
	s := newTestOIDCStateStore(t)

	key, err := s.Store(auth.OIDCFlowState{
		MCPClientID:    "client-1",
		MCPRedirectURI: "https://example.com/callback",
		IssuedAt:       time.Now().Add(-11 * time.Minute), // older than 10m TTL
	})
	require.NoError(t, err)

	result, ok := s.PeekInfo(key)
	assert.False(t, ok)
	assert.Empty(t, result.ClientID)
	assert.Empty(t, result.RedirectURI)
	assert.Nil(t, result.Scopes)

	// Expired entry should have been cleaned up by PeekInfo.
	_, ok = s.Consume(key)
	assert.False(t, ok)
}

func TestOIDCStateStore_PeekInfo_NotFound(t *testing.T) {
	s := newTestOIDCStateStore(t)

	result, ok := s.PeekInfo("missing-key")
	assert.False(t, ok)
	assert.Empty(t, result.ClientID)
	assert.Empty(t, result.RedirectURI)
	assert.Nil(t, result.Scopes)
}

// -----------------------------------------------------------------------
// fakeConsentStore - test double for ConsentCodeConsumer
// -----------------------------------------------------------------------

type fakeConsentStore struct {
	entries map[string]auth.ConsentEntry
}

func newFakeConsentStore() *fakeConsentStore {
	return &fakeConsentStore{entries: make(map[string]auth.ConsentEntry)}
}

func (s *fakeConsentStore) Store(code string, entry auth.ConsentEntry) {
	s.entries[code] = entry
}

func (s *fakeConsentStore) Consume(code string) (auth.ConsentEntry, bool) {
	entry, ok := s.entries[code]
	if !ok {
		return auth.ConsentEntry{}, false
	}
	delete(s.entries, code)
	return entry, true
}

// newConsentOIDCHandler creates an OIDCHandler with consent store wired for testing.
func newConsentOIDCHandler(t *testing.T, consentStore auth.ConsentCodeConsumer) (*auth.OIDCHandler, *auth.OIDCStateStore, *auth.CodeStore) {
	t.Helper()
	signer := newTestSigner(t)
	codeStore := newTestStore(t)
	stateStore := newTestOIDCStateStore(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore:      consentStore,
		StateStore:        stateStore,
		CodeStore:         codeStore,
		Signer:            signer,
		BaseURL:           "https://demo.meridianhub.cloud",
		BaseDomain:        "demo.meridianhub.cloud",
		DefaultTenantSlug: "acme",
		Logger:            slog.Default(),
	})
	require.NoError(t, err)
	return handler, stateStore, codeStore
}

// -----------------------------------------------------------------------
// Task 3: HandleConsentInfo
// -----------------------------------------------------------------------

func TestOIDCHandler_HandleConsentInfo_Success(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:     "meridian-mcp",
		MCPRedirectURI:  "https://claude.ai/callback",
		RequestedScopes: []string{"mcp:default", "mcp:admin"},
		IssuedAt:        time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/consent-info?client_id=meridian-mcp&mcp_state="+stateKey, nil)
	w := httptest.NewRecorder()
	handler.HandleConsentInfo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp auth.ConsentInfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, "meridian-mcp", resp.ClientID)
	assert.Equal(t, "Meridian CLI", resp.ClientName)
	assert.Equal(t, "https://claude.ai/callback", resp.RedirectURI)
	assert.Equal(t, []string{"mcp:default", "mcp:admin"}, resp.Scopes)
	assert.False(t, resp.IsDynamic)
}

func TestOIDCHandler_HandleConsentInfo_InvalidState(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, _, _ := newConsentOIDCHandler(t, consentStore)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/consent-info?client_id=meridian-mcp&mcp_state=expired-key", nil)
	w := httptest.NewRecorder()
	handler.HandleConsentInfo(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid or expired state")
}

func TestOIDCHandler_HandleConsentInfo_ClientMismatch(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:    "meridian-mcp",
		MCPRedirectURI: "https://claude.ai/callback",
		IssuedAt:       time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/consent-info?client_id=wrong-client&mcp_state="+stateKey, nil)
	w := httptest.NewRecorder()
	handler.HandleConsentInfo(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "client_id mismatch")
}

func TestOIDCHandler_HandleConsentInfo_DynamicClient(t *testing.T) {
	registry := newTestRegistry(t)

	client, err := registry.Register(auth.RegisteredClient{
		ClientName:   "My Cool App",
		RedirectURIs: []string{"https://myapp.com/callback"},
	})
	require.NoError(t, err)

	consentStore := newFakeConsentStore()
	stateStore := newTestOIDCStateStore(t)

	handler, err := auth.NewOIDCHandler(auth.OIDCHandlerConfig{
		OAuth: auth.OAuthConfig{
			ClientID:    "meridian-mcp",
			RedirectURI: "https://claude.ai/callback",
		},
		ConsentStore: consentStore,
		Registry:     registry,
		StateStore:   stateStore,
		CodeStore:    newTestStore(t),
		Signer:       newTestSigner(t),
		BaseURL:      "https://demo.meridianhub.cloud",
		BaseDomain:   "demo.meridianhub.cloud",
		Logger:       slog.Default(),
	})
	require.NoError(t, err)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:    client.ClientID,
		MCPRedirectURI: "https://myapp.com/callback",
		IssuedAt:       time.Now(),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/oauth/consent-info?client_id="+client.ClientID+"&mcp_state="+stateKey, nil)
	w := httptest.NewRecorder()
	handler.HandleConsentInfo(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp auth.ConsentInfoResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, "My Cool App", resp.ClientName)
	assert.True(t, resp.IsDynamic)
}

// -----------------------------------------------------------------------
// Task 3: HandleCallback with consent code
// -----------------------------------------------------------------------

func TestOIDCHandler_HandleCallback_ConsentCode(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, codeStore := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		MCPState:         "client-state-789",
		TenantSlug:       "acme",
		RequestedScopes:  []string{"mcp:default"},
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	consentStore.Store("consent-code-123", auth.ConsentEntry{
		Email:          "admin@acme.com",
		TenantID:       "550e8400-e29b-41d4-a716-446655440000",
		TenantSlug:     "acme",
		MCPState:       stateKey,
		ClientID:       "meridian-mcp",
		ApprovedScopes: []string{"mcp:default"},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"consent-code-123"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	assert.Equal(t, "claude.ai", redirectURL.Host)
	assert.NotEmpty(t, redirectURL.Query().Get("code"))
	assert.Equal(t, "client-state-789", redirectURL.Query().Get("state"))

	// Verify the MCP code is in the code store with correct JWT
	mcpCode := redirectURL.Query().Get("code")
	entry, ok := codeStore.Consume(mcpCode)
	require.True(t, ok)
	assert.NotEmpty(t, entry.Token)
}

func TestOIDCHandler_HandleCallback_ExpiredConsentCode(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:    "meridian-mcp",
		MCPRedirectURI: "https://claude.ai/callback",
		TenantSlug:     "acme",
		IssuedAt:       time.Now(),
	})
	require.NoError(t, err)

	// Don't store a consent code - it should fail as "not found"
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"nonexistent-consent-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid or expired consent code")
}

func TestOIDCHandler_HandleCallback_ConsentCodeMismatch(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:    "meridian-mcp",
		MCPRedirectURI: "https://claude.ai/callback",
		TenantSlug:     "acme",
		IssuedAt:       time.Now(),
	})
	require.NoError(t, err)

	// Consent code has a different mcp_state
	consentStore.Store("mismatched-code", auth.ConsentEntry{
		Email:      "admin@acme.com",
		TenantID:   "tid",
		TenantSlug: "acme",
		MCPState:   "different-state-key",
		ClientID:   "meridian-mcp",
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"mismatched-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "consent code binding mismatch")
}

func TestOIDCHandler_HandleCallback_TenantMismatch(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:    "meridian-mcp",
		MCPRedirectURI: "https://claude.ai/callback",
		TenantSlug:     "acme",
		IssuedAt:       time.Now(),
	})
	require.NoError(t, err)

	consentStore.Store("tenant-mismatch-code", auth.ConsentEntry{
		Email:      "admin@evil.com",
		TenantID:   "evil-tid",
		TenantSlug: "evil-corp", // Does not match flowState.TenantSlug
		MCPState:   stateKey,
		ClientID:   "meridian-mcp",
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"tenant-mismatch-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "tenant mismatch")
}

func TestOIDCHandler_HandleCallback_ScopeEscalation(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPClientID:     "meridian-mcp",
		MCPRedirectURI:  "https://claude.ai/callback",
		TenantSlug:      "acme",
		RequestedScopes: []string{"mcp:default"},
		IssuedAt:        time.Now(),
	})
	require.NoError(t, err)

	// Consent code has broader scopes than originally requested
	consentStore.Store("escalated-code", auth.ConsentEntry{
		Email:          "admin@acme.com",
		TenantID:       "tid",
		TenantSlug:     "acme",
		MCPState:       stateKey,
		ClientID:       "meridian-mcp",
		ApprovedScopes: []string{"mcp:default", "mcp:admin"}, // admin was not requested
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"escalated-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "approved scopes exceed requested scopes")
}

func TestOIDCHandler_HandleCallback_ScopesInJWT(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, codeStore := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	stateKey, err := stateStore.Store(auth.OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      "meridian-mcp",
		MCPRedirectURI:   "https://claude.ai/callback",
		TenantSlug:       "acme",
		RequestedScopes:  []string{"mcp:default", "mcp:admin"},
		IssuedAt:         time.Now(),
	})
	require.NoError(t, err)

	consentStore.Store("scoped-consent-code", auth.ConsentEntry{
		Email:          "admin@acme.com",
		TenantID:       "acme",
		TenantSlug:     "acme",
		MCPState:       stateKey,
		ClientID:       "meridian-mcp",
		ApprovedScopes: []string{"mcp:default", "mcp:admin"},
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/callback?"+url.Values{
		"code":  {"scoped-consent-code"},
		"state": {stateKey},
	}.Encode(), nil)
	w := httptest.NewRecorder()
	handler.HandleCallback(w, req)

	require.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	mcpCode := redirectURL.Query().Get("code")
	entry, ok := codeStore.Consume(mcpCode)
	require.True(t, ok)

	// Parse JWT and verify scopes claim
	signer := newTestSigner(t)
	_ = signer // We need the signer that was used to create the handler
	// Instead, decode the JWT payload directly (like extractEmailFromJWT)
	parts := strings.Split(entry.Token, ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &claims))

	scopesClaim, ok := claims["scopes"].([]interface{})
	require.True(t, ok, "scopes claim must be an array")
	assert.Len(t, scopesClaim, 2)
	assert.Equal(t, "mcp:default", scopesClaim[0])
	assert.Equal(t, "mcp:admin", scopesClaim[1])
}

// -----------------------------------------------------------------------
// Task 4: HandleAuthorize redirects to consent page
// -----------------------------------------------------------------------

func TestOIDCHandler_HandleAuthorize_RedirectsToConsent(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, _, _ := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"client-state-abc"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	resp := w.Result()
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Should redirect to consent page, NOT Dex
	assert.Equal(t, "/auth/mcp-consent", redirectURL.Path)
	assert.NotEmpty(t, redirectURL.Query().Get("mcp_state"))
	assert.Equal(t, "meridian-mcp", redirectURL.Query().Get("client_id"))
	// Should use tenant subdomain
	assert.Equal(t, "acme.demo.meridianhub.cloud", redirectURL.Host)
	assert.Equal(t, "https", redirectURL.Scheme)
}

func TestOIDCHandler_HandleAuthorize_StoresScopes(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"scope":                 {"mcp:read mcp:write"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	require.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	mcpState := redirectURL.Query().Get("mcp_state")
	require.NotEmpty(t, mcpState)

	// PeekInfo to verify stored scopes
	info, ok := stateStore.PeekInfo(mcpState)
	require.True(t, ok)
	assert.Equal(t, []string{"mcp:read", "mcp:write"}, info.Scopes)
}

func TestOIDCHandler_HandleAuthorize_DefaultScopes(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, stateStore, _ := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	// No scope parameter - should default to ["mcp:default"]
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	req.Host = "acme.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	require.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	mcpState := redirectURL.Query().Get("mcp_state")
	require.NotEmpty(t, mcpState)

	info, ok := stateStore.PeekInfo(mcpState)
	require.True(t, ok)
	assert.Equal(t, []string{"mcp:default"}, info.Scopes)
}

func TestOIDCHandler_HandleAuthorize_TenantScopedURL(t *testing.T) {
	consentStore := newFakeConsentStore()
	handler, _, _ := newConsentOIDCHandler(t, consentStore)

	_, challenge := generatePKCEPair(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/authorize?"+url.Values{
		"response_type":         {"code"},
		"client_id":             {"meridian-mcp"},
		"redirect_uri":          {"https://claude.ai/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode(), nil)
	req.Host = "volterra.demo.meridianhub.cloud"
	w := httptest.NewRecorder()
	handler.HandleAuthorize(w, req)

	require.Equal(t, http.StatusFound, w.Code)

	location := w.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	assert.Equal(t, "volterra.demo.meridianhub.cloud", redirectURL.Host)
	assert.Equal(t, "/auth/mcp-consent", redirectURL.Path)
}
