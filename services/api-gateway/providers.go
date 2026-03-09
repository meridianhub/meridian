package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// AuthProvider represents a configured authentication provider returned by the
// provider discovery endpoint. The frontend uses this to render login buttons.
type AuthProvider struct {
	// ID is the unique identifier for this provider (e.g., "meridian", "google").
	ID string `json:"id"`
	// Type is the provider type: "password" for local login, "oidc" for external.
	Type string `json:"type"`
	// DisplayName is the human-readable label for the login button.
	DisplayName string `json:"displayName"`
	// AuthURL is the Dex authorization URL for OIDC providers.
	// Empty for password-type providers.
	AuthURL string `json:"authUrl,omitempty"`
}

// ProvidersResponse is the JSON response for GET /api/auth/providers.
type ProvidersResponse struct {
	Providers []AuthProvider `json:"providers"`
}

// ProvidersConfig holds the configuration for the provider discovery endpoint.
type ProvidersConfig struct {
	// Enabled controls whether the /api/auth/providers endpoint is registered.
	Enabled bool
	// Providers is the list of configured authentication providers.
	Providers []AuthProvider
}

// LoadProvidersConfig loads provider configuration from environment variables.
//
// Environment variables:
//   - AUTH_PROVIDERS_ENABLED: Enable the /api/auth/providers endpoint (default: false)
//   - AUTH_PROVIDERS: JSON array of provider objects
//   - DEX_ISSUER: Used to construct authUrl for OIDC providers when not explicitly set
//
// When AUTH_PROVIDERS is not set but AUTH_PROVIDERS_ENABLED is true, a default
// "meridian" password provider is included.
func LoadProvidersConfig() ProvidersConfig {
	config := ProvidersConfig{
		Enabled: getEnvBool("AUTH_PROVIDERS_ENABLED", false),
	}

	if !config.Enabled {
		return config
	}

	dexIssuer := strings.TrimRight(os.Getenv("DEX_ISSUER"), "/")

	if raw := os.Getenv("AUTH_PROVIDERS"); raw != "" {
		var providers []AuthProvider
		if err := json.Unmarshal([]byte(raw), &providers); err != nil {
			slog.Error("failed to parse AUTH_PROVIDERS, falling back to default provider",
				"error", err)
		} else {
			// Populate authUrl for OIDC providers that don't have one set
			for i := range providers {
				if providers[i].Type == "oidc" && providers[i].AuthURL == "" && dexIssuer != "" {
					providers[i].AuthURL = dexIssuer + "/auth/" + providers[i].ID
				}
			}
			config.Providers = providers
		}
	}

	// Default to just the local password provider if nothing configured
	if len(config.Providers) == 0 {
		config.Providers = []AuthProvider{
			{ID: "meridian", Type: "password", DisplayName: "Email & Password"},
		}
	}

	return config
}

// getEnvBool parses a boolean from an environment variable.
func getEnvBool(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultVal
	}
}

// handleProviders returns the configured authentication providers as JSON.
// This endpoint is public (no auth required) because the frontend needs
// to know which login methods are available before the user authenticates.
func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")

	resp := ProvidersResponse{Providers: s.providersConfig.Providers}
	if resp.Providers == nil {
		resp.Providers = []AuthProvider{}
	}

	_ = json.NewEncoder(w).Encode(resp)
}
