package auth_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRegistry(t *testing.T) *auth.ClientRegistry {
	t.Helper()
	r := auth.NewClientRegistry()
	t.Cleanup(r.Close)
	return r
}

func TestRegistrationHandler_RegistersClient(t *testing.T) {
	registry := newTestRegistry(t)
	handler := auth.NewRegistrationHandler(registry, slog.Default())

	body := `{"client_name":"test-client","redirect_uris":["https://example.com/callback"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var client auth.RegisteredClient
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &client))
	assert.NotEmpty(t, client.ClientID)
	assert.Equal(t, "test-client", client.ClientName)
	assert.Equal(t, []string{"https://example.com/callback"}, client.RedirectURIs)
	assert.Equal(t, []string{"authorization_code"}, client.GrantTypes)
	assert.Equal(t, []string{"code"}, client.ResponseTypes)
	assert.Equal(t, "none", client.TokenEndpointAuthMethod)

	// Verify client is in the registry.
	looked, ok := registry.Lookup(client.ClientID)
	assert.True(t, ok)
	assert.Equal(t, client.ClientID, looked.ClientID)
}

func TestRegistrationHandler_RejectsNoRedirectURIs(t *testing.T) {
	registry := newTestRegistry(t)
	handler := auth.NewRegistrationHandler(registry, slog.Default())

	body := `{"client_name":"test"}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegistrationHandler_RejectsHTTPRedirectURI(t *testing.T) {
	registry := newTestRegistry(t)
	handler := auth.NewRegistrationHandler(registry, slog.Default())

	body := `{"redirect_uris":["http://evil.com/callback"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegistrationHandler_AllowsLocalhostHTTP(t *testing.T) {
	registry := newTestRegistry(t)
	handler := auth.NewRegistrationHandler(registry, slog.Default())

	body := `{"redirect_uris":["http://localhost:12345/callback"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestRegistrationHandler_MethodNotAllowed(t *testing.T) {
	registry := newTestRegistry(t)
	handler := auth.NewRegistrationHandler(registry, slog.Default())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/oauth/register", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestClientRegistry_LookupUnknown(t *testing.T) {
	registry := newTestRegistry(t)
	_, ok := registry.Lookup("nonexistent")
	assert.False(t, ok)
}

func TestRegisteredClient_HasRedirectURI(t *testing.T) {
	client := auth.RegisteredClient{
		RedirectURIs: []string{"https://a.com/cb", "https://b.com/cb"},
	}
	assert.True(t, client.HasRedirectURI("https://a.com/cb"))
	assert.True(t, client.HasRedirectURI("https://b.com/cb"))
	assert.False(t, client.HasRedirectURI("https://c.com/cb"))
}

func TestMetadataHandler_IncludesRegistrationEndpoint(t *testing.T) {
	cfg := auth.OAuthConfig{
		ClientID:         "meridian-mcp",
		AuthorizationURL: "https://mcp.example.com/oauth/authorize",
		TokenURL:         "https://mcp.example.com/oauth/token",
	}

	handler := auth.NewMetadataHandler("https://mcp.example.com", cfg)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	var meta auth.AuthorizationServerMetadata
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))
	assert.Equal(t, "https://mcp.example.com/oauth/register", meta.RegistrationEndpoint)
}
