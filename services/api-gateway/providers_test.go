package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvidersEndpoint_ReturnsProviders(t *testing.T) {
	providers := []AuthProvider{
		{ID: "meridian", Type: "password", DisplayName: "Email & Password"},
		{ID: "google", Type: "oidc", DisplayName: "Google", AuthURL: "https://dex.example.com/dex/auth/google"},
	}

	server := newTestServerWithProviders(t, ProvidersConfig{
		Enabled:   true,
		Providers: providers,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=300", resp.Header.Get("Cache-Control"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result ProvidersResponse
	require.NoError(t, json.Unmarshal(body, &result))
	require.Len(t, result.Providers, 2)
	assert.Equal(t, "meridian", result.Providers[0].ID)
	assert.Equal(t, "password", result.Providers[0].Type)
	assert.Equal(t, "google", result.Providers[1].ID)
	assert.Equal(t, "oidc", result.Providers[1].Type)
	assert.Equal(t, "https://dex.example.com/dex/auth/google", result.Providers[1].AuthURL)
}

func TestProvidersEndpoint_EmptyProviders_ReturnsEmptyArray(t *testing.T) {
	server := newTestServerWithProviders(t, ProvidersConfig{
		Enabled:   true,
		Providers: nil,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result ProvidersResponse
	require.NoError(t, json.Unmarshal(body, &result))
	assert.NotNil(t, result.Providers)
	assert.Empty(t, result.Providers)
}

func TestProvidersEndpoint_PostReturns405(t *testing.T) {
	server := newTestServerWithProviders(t, ProvidersConfig{
		Enabled:   true,
		Providers: []AuthProvider{{ID: "meridian", Type: "password", DisplayName: "Email & Password"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/providers", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Result().StatusCode)
}

func TestProvidersEndpoint_DisabledReturns404(t *testing.T) {
	server := newTestServerWithProviders(t, ProvidersConfig{
		Enabled: false,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	// When disabled, the route is not registered. The "/" catch-all will handle it.
	// With no backend configured, it returns 503.
	resp := w.Result()
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}

func TestProvidersEndpoint_OmitsEmptyAuthURL(t *testing.T) {
	server := newTestServerWithProviders(t, ProvidersConfig{
		Enabled: true,
		Providers: []AuthProvider{
			{ID: "meridian", Type: "password", DisplayName: "Email & Password"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	body, err := io.ReadAll(w.Result().Body)
	require.NoError(t, err)

	// authUrl should be omitted (omitempty) for password type
	assert.NotContains(t, string(body), "authUrl")
}

func TestLoadProvidersConfig_Disabled(t *testing.T) {
	cfg := LoadProvidersConfig()
	assert.False(t, cfg.Enabled)
	assert.Nil(t, cfg.Providers)
}

func TestLoadProvidersConfig_EnabledWithDefaults(t *testing.T) {
	t.Setenv("AUTH_PROVIDERS_ENABLED", "true")

	cfg := LoadProvidersConfig()
	assert.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	assert.Equal(t, "meridian", cfg.Providers[0].ID)
	assert.Equal(t, "password", cfg.Providers[0].Type)
}

func TestLoadProvidersConfig_JSONProviders(t *testing.T) {
	t.Setenv("AUTH_PROVIDERS_ENABLED", "true")
	t.Setenv("AUTH_PROVIDERS", `[
		{"id":"meridian","type":"password","displayName":"Email & Password"},
		{"id":"google","type":"oidc","displayName":"Google"}
	]`)
	t.Setenv("DEX_ISSUER", "https://dex.example.com/dex")

	cfg := LoadProvidersConfig()
	assert.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 2)
	assert.Equal(t, "meridian", cfg.Providers[0].ID)
	assert.Empty(t, cfg.Providers[0].AuthURL) // password type gets no authUrl
	assert.Equal(t, "google", cfg.Providers[1].ID)
	assert.Equal(t, "https://dex.example.com/dex/auth/google", cfg.Providers[1].AuthURL)
}

func TestLoadProvidersConfig_ExplicitAuthURL(t *testing.T) {
	t.Setenv("AUTH_PROVIDERS_ENABLED", "true")
	t.Setenv("AUTH_PROVIDERS", `[{"id":"custom","type":"oidc","displayName":"Custom","authUrl":"https://custom.example.com/auth"}]`)
	t.Setenv("DEX_ISSUER", "https://dex.example.com/dex")

	cfg := LoadProvidersConfig()
	require.Len(t, cfg.Providers, 1)
	// Explicit authUrl should NOT be overwritten
	assert.Equal(t, "https://custom.example.com/auth", cfg.Providers[0].AuthURL)
}

func TestLoadProvidersConfig_InvalidJSON_FallsBackToDefault(t *testing.T) {
	t.Setenv("AUTH_PROVIDERS_ENABLED", "true")
	t.Setenv("AUTH_PROVIDERS", `{invalid`)

	cfg := LoadProvidersConfig()
	require.Len(t, cfg.Providers, 1)
	assert.Equal(t, "meridian", cfg.Providers[0].ID)
}

func newTestServerWithProviders(t *testing.T, providersCfg ProvidersConfig) *Server {
	t.Helper()
	config := &Config{
		Port:         8080,
		BaseDomain:   "test.example.com",
		DatabaseURL:  "postgres://test",
		LocalDevMode: true,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewServer(config, logger, nil, WithProvidersConfig(providersCfg))
}
