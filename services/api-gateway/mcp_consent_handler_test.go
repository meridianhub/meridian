package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// mockOIDCStatePeeker implements OIDCStatePeeker for tests.
type mockOIDCStatePeeker struct {
	mu      sync.Mutex
	entries map[string]OIDCStatePeekResult
}

func newMockOIDCStatePeeker() *mockOIDCStatePeeker {
	return &mockOIDCStatePeeker{entries: make(map[string]OIDCStatePeekResult)}
}

func (m *mockOIDCStatePeeker) addEntry(key string, result OIDCStatePeekResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[key] = result
}

func (m *mockOIDCStatePeeker) PeekInfo(key string) (OIDCStatePeekResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return OIDCStatePeekResult{}, false
	}
	return e, true
}

func (m *mockOIDCStatePeeker) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
}

func (m *mockOIDCStatePeeker) hasEntry(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.entries[key]
	return ok
}

func newTestMCPConsentHandler(t *testing.T) (*MCPConsentHandler, *ConsentCodeStore, *mockOIDCStatePeeker) {
	t.Helper()
	consentStore := NewConsentCodeStore()
	t.Cleanup(consentStore.Close)

	oidcStore := newMockOIDCStatePeeker()

	handler := NewMCPConsentHandler(MCPConsentHandlerConfig{
		ConsentStore:   consentStore,
		OIDCStateStore: oidcStore,
		Logger:         slog.Default(),
	})
	return handler, consentStore, oidcStore
}

func withAuthContext(ctx context.Context, email, tenantID, tenantSlug string) context.Context {
	claims := &platformauth.Claims{
		Email:    email,
		TenantID: tenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: email,
		},
	}
	ctx = context.WithValue(ctx, auth.ClaimsContextKey, claims)
	ctx = tenant.WithSlug(ctx, tenantSlug)
	return ctx
}

func doConsentRequest(t *testing.T, handler *MCPConsentHandler, ctx context.Context, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/mcp-consent", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.HandleConsent(rr, req)
	return rr
}

func TestMCPConsentHandler_Approve(t *testing.T) {
	handler, _, oidcStore := newTestMCPConsentHandler(t)

	stateKey := "test-state-key"
	oidcStore.addEntry(stateKey, OIDCStatePeekResult{
		ClientID: "test-client", RedirectURI: "https://example.com/callback",
		Scopes: []string{"mcp:default"}, MCPState: "client-original-state", TenantSlug: "acme",
	})
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
		MCPState: stateKey,
		ClientID: "test-client",
		Action:   "approve",
	})

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp mcpConsentResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Contains(t, resp.RedirectURL, "/oauth/callback")
	assert.Contains(t, resp.RedirectURL, "code=")
	assert.Contains(t, resp.RedirectURL, "state="+stateKey)

	// OIDC state should not be consumed on approve (callback will consume it).
	assert.True(t, oidcStore.hasEntry(stateKey), "OIDC state should not be consumed on approve")
}

func TestMCPConsentHandler_Deny(t *testing.T) {
	handler, _, oidcStore := newTestMCPConsentHandler(t)

	stateKey := "test-state-deny"
	oidcStore.addEntry(stateKey, OIDCStatePeekResult{
		ClientID: "test-client", RedirectURI: "https://example.com/callback",
		MCPState: "client-original-state", TenantSlug: "acme",
	})
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
		MCPState: stateKey,
		ClientID: "test-client",
		Action:   "deny",
	})

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp mcpConsentResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Contains(t, resp.RedirectURL, "error=access_denied")
	assert.Contains(t, resp.RedirectURL, "state=client-original-state")

	// OIDC state should be deleted on deny.
	assert.False(t, oidcStore.hasEntry(stateKey), "OIDC state should be deleted on deny")
}

func TestMCPConsentHandler_InvalidAction(t *testing.T) {
	handler, _, oidcStore := newTestMCPConsentHandler(t)

	stateKey := "test-state-action"
	oidcStore.addEntry(stateKey, OIDCStatePeekResult{
		ClientID: "test-client", RedirectURI: "https://example.com/callback",
		MCPState: "client-original-state", TenantSlug: "acme",
	})
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	for _, action := range []string{"", "maybe", "APPROVE"} {
		t.Run("action="+action, func(t *testing.T) {
			rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
				MCPState: stateKey,
				ClientID: "test-client",
				Action:   action,
			})

			var resp mcpConsentErrorResponse
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
			assert.Equal(t, http.StatusBadRequest, rr.Code)
			assert.Equal(t, "invalid_action", resp.Error)
		})
	}
}

func TestMCPConsentHandler_MethodNotAllowed(t *testing.T) {
	handler, _, _ := newTestMCPConsentHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/mcp-consent", nil)
	rr := httptest.NewRecorder()
	handler.HandleConsent(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "POST", rr.Header().Get("Allow"))
}

func TestMCPConsentHandler_InvalidState(t *testing.T) {
	handler, _, _ := newTestMCPConsentHandler(t)
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
		MCPState: "nonexistent-state",
		ClientID: "test-client",
		Action:   "approve",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp mcpConsentErrorResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "invalid_state", resp.Error)
}

func TestMCPConsentHandler_ClientMismatch(t *testing.T) {
	handler, _, oidcStore := newTestMCPConsentHandler(t)

	stateKey := "test-state-mismatch"
	oidcStore.addEntry(stateKey, OIDCStatePeekResult{
		ClientID: "real-client", RedirectURI: "https://example.com/callback",
		TenantSlug: "acme",
	})
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
		MCPState: stateKey,
		ClientID: "wrong-client",
		Action:   "approve",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp mcpConsentErrorResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "client_mismatch", resp.Error)
}

func TestMCPConsentHandler_TenantMismatch(t *testing.T) {
	handler, _, oidcStore := newTestMCPConsentHandler(t)

	stateKey := "test-state-tenant"
	oidcStore.addEntry(stateKey, OIDCStatePeekResult{
		ClientID: "test-client", RedirectURI: "https://example.com/callback",
		TenantSlug: "other-tenant",
	})
	// JWT tenant slug is "acme" but state has "other-tenant"
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	rr := doConsentRequest(t, handler, ctx, mcpConsentRequest{
		MCPState: stateKey,
		ClientID: "test-client",
		Action:   "approve",
	})

	assert.Equal(t, http.StatusForbidden, rr.Code)

	var resp mcpConsentErrorResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "tenant_mismatch", resp.Error)
}

func TestMCPConsentHandler_InvalidJSON(t *testing.T) {
	handler, _, _ := newTestMCPConsentHandler(t)
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/mcp-consent", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.HandleConsent(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp mcpConsentErrorResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "invalid_json", resp.Error)
}

func TestMCPConsentHandler_MissingFields(t *testing.T) {
	handler, _, _ := newTestMCPConsentHandler(t)
	ctx := withAuthContext(context.Background(), "alice@example.com", "tenant-uuid-1", "acme")

	tests := []struct {
		name          string
		body          mcpConsentRequest
		expectedError string
	}{
		{"missing mcp_state", mcpConsentRequest{ClientID: "c", Action: "approve"}, "missing_fields"},
		{"missing client_id", mcpConsentRequest{MCPState: "s", Action: "approve"}, "missing_fields"},
		{"missing both", mcpConsentRequest{Action: "approve"}, "missing_fields"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := doConsentRequest(t, handler, ctx, tt.body)

			assert.Equal(t, http.StatusBadRequest, rr.Code)

			var resp mcpConsentErrorResponse
			require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
			assert.Equal(t, tt.expectedError, resp.Error)
		})
	}
}
