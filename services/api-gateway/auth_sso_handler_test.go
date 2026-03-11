package gateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/identity/connector"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubResolver is a test implementation of gateway.IdentityResolver.
type stubResolver struct {
	resolveFn func(ctx context.Context, email string) (connector.Identity, bool, error)
}

func (s *stubResolver) Resolve(ctx context.Context, email string) (connector.Identity, bool, error) {
	return s.resolveFn(ctx, email)
}

func newSSOTestSigner(t *testing.T) *platformauth.JWTSigner {
	t.Helper()
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		KeyID:  "test-sso-1",
		Issuer: "test-meridian",
	})
	require.NoError(t, err)
	return signer
}

func newTestSSOHandler(t *testing.T, dexURL string, resolver gateway.IdentityResolver, httpClient *http.Client) *gateway.SSOHandler {
	t.Helper()
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexURL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   httpClient,
		StateStore:   gateway.NewStateStore(5 * time.Minute),
	})
	require.NoError(t, err)
	return handler
}

// --- Constructor validation tests ---

func TestNewSSOHandler_Validation(t *testing.T) {
	signer := newSSOTestSigner(t)
	resolver := &stubResolver{}
	logger := slog.Default()

	tests := []struct {
		name    string
		cfg     gateway.SSOHandlerConfig
		wantErr error
	}{
		{
			name:    "missing dex issuer",
			cfg:     gateway.SSOHandlerConfig{ClientID: "c", Signer: signer, Resolver: resolver, Logger: logger},
			wantErr: gateway.ErrSSODexIssuerRequired,
		},
		{
			name:    "missing client ID",
			cfg:     gateway.SSOHandlerConfig{DexIssuerURL: "http://dex", Signer: signer, Resolver: resolver, Logger: logger},
			wantErr: gateway.ErrSSOClientIDRequired,
		},
		{
			name:    "missing signer",
			cfg:     gateway.SSOHandlerConfig{DexIssuerURL: "http://dex", ClientID: "c", Resolver: resolver, Logger: logger},
			wantErr: gateway.ErrSSOSignerRequired,
		},
		{
			name:    "missing resolver",
			cfg:     gateway.SSOHandlerConfig{DexIssuerURL: "http://dex", ClientID: "c", Signer: signer, Logger: logger},
			wantErr: gateway.ErrSSOResolverRequired,
		},
		{
			name:    "missing logger",
			cfg:     gateway.SSOHandlerConfig{DexIssuerURL: "http://dex", ClientID: "c", Signer: signer, Resolver: resolver},
			wantErr: gateway.ErrSSOLoggerRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gateway.NewSSOHandler(tt.cfg)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// --- HandleInitiate tests ---

func TestSSOHandler_InitiateRedirectsToDex(t *testing.T) {
	handler := newTestSSOHandler(t, "https://demo.meridianhub.cloud/dex", &stubResolver{}, nil)

	mux := http.NewServeMux()
	mux.Handle("GET /api/auth/sso/{connector_id}", http.HandlerFunc(handler.HandleInitiate))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/google?return_url=/dashboard", nil)
	tid, _ := tenant.NewTenantID("volterra")
	req = req.WithContext(tenant.WithTenant(req.Context(), tid))
	req.SetPathValue("connector_id", "google")

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	require.NotEmpty(t, location)

	u, err := url.Parse(location)
	require.NoError(t, err)

	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "demo.meridianhub.cloud", u.Host)
	assert.Equal(t, "/dex/auth/google", u.Path)

	params := u.Query()
	assert.Equal(t, "meridian-service", params.Get("client_id"))
	assert.Equal(t, "https://demo.meridianhub.cloud/api/auth/callback", params.Get("redirect_uri"))
	assert.Equal(t, "code", params.Get("response_type"))
	assert.Equal(t, "openid email profile", params.Get("scope"))
	assert.Equal(t, "S256", params.Get("code_challenge_method"))
	assert.NotEmpty(t, params.Get("state"))
	assert.NotEmpty(t, params.Get("code_challenge"))
}

func TestSSOHandler_InitiateNoTenant(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/google", nil)
	req.SetPathValue("connector_id", "google")
	// No tenant context

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSSOHandler_InitiateMissingConnectorID(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/", nil)
	tid, _ := tenant.NewTenantID("volterra")
	req = req.WithContext(tenant.WithTenant(req.Context(), tid))
	// No path value set for connector_id

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSSOHandler_InitiateMethodNotAllowed(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sso/google", nil)
	req.SetPathValue("connector_id", "google")

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// --- HandleCallback tests ---

func TestSSOHandler_CallbackSuccess(t *testing.T) {
	// Set up a fake Dex token endpoint that returns a mock ID token.
	fakeEmail := "alice@volterra.energy"
	idToken := buildFakeIDToken(t, fakeEmail)

	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		err := r.ParseForm()
		require.NoError(t, err)
		assert.Equal(t, "authorization_code", r.FormValue("grant_type"))
		assert.Equal(t, "meridian-service", r.FormValue("client_id"))
		assert.NotEmpty(t, r.FormValue("code_verifier"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":     idToken,
			"access_token": "dex-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer dexServer.Close()

	resolver := &stubResolver{
		resolveFn: func(_ context.Context, email string) (connector.Identity, bool, error) {
			if email == fakeEmail {
				return connector.Identity{
					UserID:   "user-alice",
					Username: "Alice",
					Email:    fakeEmail,
					Groups:   []string{"operator"},
				}, true, nil
			}
			return connector.Identity{}, false, nil
		},
	}

	stateStore := gateway.NewStateStore(5 * time.Minute)
	signer := newSSOTestSigner(t)

	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       signer,
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	// Pre-populate state (simulating what HandleInitiate would do).
	tid, _ := tenant.NewTenantID("volterra")
	stateKey, err := stateStore.Set(gateway.StateData{
		CodeVerifier: "test-verifier-abc",
		TenantID:     tid,
		ReturnURL:    "/dashboard",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=auth-code-123", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	require.NotEmpty(t, location)
	assert.Contains(t, location, "/dashboard")
	assert.Contains(t, location, "access_token=")
}

func TestSSOHandler_CallbackInvalidState(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=bogus&code=abc", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	assert.Equal(t, "invalid or expired state parameter", resp["error"])
}

func TestSSOHandler_CallbackMissingParams(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	tests := []struct {
		name  string
		query string
	}{
		{"missing both", ""},
		{"missing code", "?state=abc"},
		{"missing state", "?code=abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/callback"+tt.query, nil)
			rec := httptest.NewRecorder()
			handler.HandleCallback(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestSSOHandler_CallbackDexError(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?error=access_denied&error_description=user+denied", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	assert.Contains(t, resp["error"], "access_denied")
}

func TestSSOHandler_CallbackIdentityNotFound(t *testing.T) {
	fakeEmail := "unknown@volterra.energy"
	idToken := buildFakeIDToken(t, fakeEmail)

	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":     idToken,
			"access_token": "dex-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer dexServer.Close()

	resolver := &stubResolver{
		resolveFn: func(_ context.Context, _ string) (connector.Identity, bool, error) {
			return connector.Identity{}, false, nil
		},
	}

	stateStore := gateway.NewStateStore(5 * time.Minute)
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	tid, _ := tenant.NewTenantID("volterra")
	stateKey, _ := stateStore.Set(gateway.StateData{
		CodeVerifier: "verifier",
		TenantID:     tid,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=code", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestSSOHandler_CallbackResolverError(t *testing.T) {
	fakeEmail := "err@volterra.energy"
	idToken := buildFakeIDToken(t, fakeEmail)

	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":     idToken,
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer dexServer.Close()

	resolver := &stubResolver{
		resolveFn: func(_ context.Context, _ string) (connector.Identity, bool, error) {
			return connector.Identity{}, false, errors.New("db connection lost")
		},
	}

	stateStore := gateway.NewStateStore(5 * time.Minute)
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	tid, _ := tenant.NewTenantID("volterra")
	stateKey, _ := stateStore.Set(gateway.StateData{
		CodeVerifier: "verifier",
		TenantID:     tid,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=code", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSSOHandler_CallbackMethodNotAllowed(t *testing.T) {
	handler := newTestSSOHandler(t, "http://dex:5556/dex", &stubResolver{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/callback?state=abc&code=xyz", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSSOHandler_CallbackTokenExchangeError(t *testing.T) {
	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":             "invalid_grant",
			"error_description": "code expired",
		})
	}))
	defer dexServer.Close()

	stateStore := gateway.NewStateStore(5 * time.Minute)
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     &stubResolver{},
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	tid, _ := tenant.NewTenantID("volterra")
	stateKey, _ := stateStore.Set(gateway.StateData{
		CodeVerifier: "verifier",
		TenantID:     tid,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=expired-code", nil)
	rec := httptest.NewRecorder()
	handler.HandleCallback(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// --- Open redirect protection tests ---

func TestSSOHandler_InitiateRejectsAbsoluteReturnURL(t *testing.T) {
	// Verifies that an absolute URL in return_url is sanitized to "/" to prevent
	// open redirect attacks that could steal the JWT from the URL fragment.
	fakeEmail := "alice@volterra.energy"
	idToken := buildFakeIDToken(t, fakeEmail)

	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":     idToken,
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer dexServer.Close()

	resolver := &stubResolver{
		resolveFn: func(_ context.Context, _ string) (connector.Identity, bool, error) {
			return connector.Identity{
				UserID: "user-1", Email: fakeEmail, Groups: []string{},
			}, true, nil
		},
	}

	stateStore := gateway.NewStateStore(5 * time.Minute)
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	// Simulate initiate with malicious return_url
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/google?return_url=https://evil.com/steal", nil)
	tid, _ := tenant.NewTenantID("volterra")
	req = req.WithContext(tenant.WithTenant(req.Context(), tid))
	req.SetPathValue("connector_id", "google")

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)

	// Extract state from the redirect to Dex
	location := rec.Header().Get("Location")
	u, err := url.Parse(location)
	require.NoError(t, err)
	stateKey := u.Query().Get("state")
	require.NotEmpty(t, stateKey)

	// Now simulate the callback with that state
	callbackReq := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=auth-code", nil)
	callbackRec := httptest.NewRecorder()
	handler.HandleCallback(callbackRec, callbackReq)

	assert.Equal(t, http.StatusFound, callbackRec.Code)

	// The redirect should go to "/" (sanitized), NOT "https://evil.com/steal"
	callbackLocation := callbackRec.Header().Get("Location")
	assert.NotContains(t, callbackLocation, "evil.com")
	assert.True(t, strings.HasPrefix(callbackLocation, "/"), "redirect should be relative, got: %s", callbackLocation)
}

func TestSSOHandler_InitiateAcceptsRelativeReturnURL(t *testing.T) {
	fakeEmail := "bob@volterra.energy"
	idToken := buildFakeIDToken(t, fakeEmail)

	dexServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id_token":     idToken,
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer dexServer.Close()

	resolver := &stubResolver{
		resolveFn: func(_ context.Context, _ string) (connector.Identity, bool, error) {
			return connector.Identity{
				UserID: "user-2", Email: fakeEmail, Groups: []string{},
			}, true, nil
		},
	}

	stateStore := gateway.NewStateStore(5 * time.Minute)
	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexServer.URL,
		ClientID:     "meridian-service",
		CallbackURL:  "https://demo.meridianhub.cloud/api/auth/callback",
		Signer:       newSSOTestSigner(t),
		Resolver:     resolver,
		Logger:       slog.Default(),
		HTTPClient:   dexServer.Client(),
		StateStore:   stateStore,
	})
	require.NoError(t, err)

	// Initiate with valid relative return_url
	req := httptest.NewRequest(http.MethodGet, "/api/auth/sso/google?return_url=/dashboard/overview", nil)
	tid, _ := tenant.NewTenantID("volterra")
	req = req.WithContext(tenant.WithTenant(req.Context(), tid))
	req.SetPathValue("connector_id", "google")

	rec := httptest.NewRecorder()
	handler.HandleInitiate(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)

	location := rec.Header().Get("Location")
	u, err := url.Parse(location)
	require.NoError(t, err)
	stateKey := u.Query().Get("state")

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state="+stateKey+"&code=auth-code", nil)
	callbackRec := httptest.NewRecorder()
	handler.HandleCallback(callbackRec, callbackReq)

	assert.Equal(t, http.StatusFound, callbackRec.Code)

	callbackLocation := callbackRec.Header().Get("Location")
	assert.Contains(t, callbackLocation, "/dashboard/overview")
}

// --- Helpers ---

// buildFakeIDToken creates a minimal unsigned JWT with an email claim.
// The SSO handler only base64-decodes the payload (no signature verification)
// since it trusts the server-to-server response from Dex.
func buildFakeIDToken(t *testing.T, email string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]string{"email": email})
	require.NoError(t, err)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + encodedPayload + ".fake-sig"
}
