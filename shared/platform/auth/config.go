package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

var (
	// ErrMissingJWKSURL is returned when JWKS_URL env var is not set
	ErrMissingJWKSURL = errors.New("JWKS_URL environment variable is required")
	// ErrInvalidAuthMode is returned when AUTH_MODE has an invalid value
	ErrInvalidAuthMode = errors.New("AUTH_MODE must be 'jwks', 'oauth', or 'disabled'")
	// ErrMissingOAuthClientID is returned when OAUTH_CLIENT_ID is not set
	ErrMissingOAuthClientID = errors.New("OAUTH_CLIENT_ID environment variable is required")
	// ErrMissingOAuthClientSecret is returned when OAUTH_CLIENT_SECRET is not set
	ErrMissingOAuthClientSecret = errors.New("OAUTH_CLIENT_SECRET environment variable is required")
	// ErrMissingOAuthTokenURL is returned when OAUTH_TOKEN_URL is not set
	ErrMissingOAuthTokenURL = errors.New("OAUTH_TOKEN_URL environment variable is required")
	// ErrOAuthModeNotSupported is returned when OAuth mode is used for inbound auth
	ErrOAuthModeNotSupported = errors.New("OAuth mode does not support inbound authentication interceptor")
	// ErrUnsupportedAuthMode is returned when an unsupported auth mode is used
	ErrUnsupportedAuthMode = errors.New("unsupported auth mode")
	// ErrInvalidModeForOAuthClient is returned when OAuth client is created in non-OAuth mode
	ErrInvalidModeForOAuthClient = errors.New("OAuth client requires auth mode 'oauth'")
	// ErrInvalidModeForIntrospector is returned when introspector is created in non-OAuth mode
	ErrInvalidModeForIntrospector = errors.New("OAuth introspector requires auth mode 'oauth'")
	// ErrMissingIntrospectionURL is returned when OAUTH_INTROSPECTION_URL is not set
	ErrMissingIntrospectionURL = errors.New("OAUTH_INTROSPECTION_URL is required for introspection")
)

// defaultHTTPClient returns an HTTP client with reasonable timeout settings.
// This prevents hanging requests by setting a timeout for auth operations.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaults.DefaultRPCTimeout,
	}
}

// AuthMode defines the authentication mode for the service
//
//nolint:revive // AuthMode is more descriptive than Mode for auth.AuthMode
type AuthMode string

const (
	// AuthModeJWKS uses JWKS-based JWT validation
	AuthModeJWKS AuthMode = "jwks"
	// AuthModeOAuth uses OAuth 2.0 client credentials flow
	AuthModeOAuth AuthMode = "oauth"
	// AuthModeDisabled disables authentication (for testing only)
	AuthModeDisabled AuthMode = "disabled"
)

// Config contains authentication configuration for a service.
// This provides a simple way to configure JWT validation with JWKS or OAuth.
type Config struct {
	// Mode determines the authentication mechanism (jwks, oauth, disabled)
	Mode AuthMode

	// JWKS configuration (when Mode = AuthModeJWKS)
	JWKSURL        string        // JWKS endpoint URL
	JWKSCacheTTL   time.Duration // How long to cache JWKS keys
	JWKSRefreshTTL time.Duration // Background refresh interval

	// OAuth configuration (when Mode = AuthModeOAuth)
	OAuthClientID     string   // OAuth client ID
	OAuthClientSecret string   // OAuth client secret
	OAuthTokenURL     string   // OAuth token endpoint
	OAuthScopes       []string // OAuth scopes to request

	// OAuth Introspection configuration (optional)
	OAuthIntrospectionURL string // Token introspection endpoint

	// HTTP client for external requests
	HTTPClient *http.Client
}

// NewConfigFromEnv creates authentication configuration from environment variables.
// This is the recommended way to configure authentication in containerized environments.
//
// Environment variables:
// - AUTH_MODE: Required. One of "jwks", "oauth", or "disabled"
//
// JWKS mode variables:
// - JWKS_URL: Required. JWKS endpoint URL (e.g., "https://auth.example.com/.well-known/jwks.json")
// - JWKS_CACHE_TTL: Optional. Cache duration (default: 24h)
// - JWKS_REFRESH_TTL: Optional. Background refresh interval (default: none)
//
// OAuth mode variables:
// - OAUTH_CLIENT_ID: Required. OAuth client ID
// - OAUTH_CLIENT_SECRET: Required. OAuth client secret
// - OAUTH_TOKEN_URL: Required. OAuth token endpoint
// - OAUTH_SCOPES: Optional. Comma-separated scopes
// - OAUTH_INTROSPECTION_URL: Optional. Token introspection endpoint
//
// Returns an error if required environment variables are missing or invalid.
func NewConfigFromEnv() (Config, error) {
	mode := AuthMode(os.Getenv("AUTH_MODE"))
	if mode == "" {
		mode = AuthModeJWKS // Default to JWKS
	}

	config := Config{
		Mode:       mode,
		HTTPClient: defaultHTTPClient(),
	}

	switch mode {
	case AuthModeJWKS:
		if err := applyJWKSConfigFromEnv(&config); err != nil {
			return Config{}, err
		}
	case AuthModeOAuth:
		if err := applyOAuthConfigFromEnv(&config); err != nil {
			return Config{}, err
		}
	case AuthModeDisabled:
		// No configuration needed
	default:
		return Config{}, fmt.Errorf("%w: got %q", ErrInvalidAuthMode, mode)
	}

	return config, nil
}

// applyJWKSConfigFromEnv populates JWKS-specific fields from environment variables.
func applyJWKSConfigFromEnv(config *Config) error {
	config.JWKSURL = os.Getenv("JWKS_URL")
	if config.JWKSURL == "" {
		return ErrMissingJWKSURL
	}

	cacheTTL := os.Getenv("JWKS_CACHE_TTL")
	if cacheTTL != "" {
		ttl, err := time.ParseDuration(cacheTTL)
		if err != nil {
			return fmt.Errorf("invalid JWKS_CACHE_TTL: %w", err)
		}
		config.JWKSCacheTTL = ttl
	} else {
		config.JWKSCacheTTL = 24 * time.Hour
	}

	refreshTTL := os.Getenv("JWKS_REFRESH_TTL")
	if refreshTTL != "" {
		ttl, err := time.ParseDuration(refreshTTL)
		if err != nil {
			return fmt.Errorf("invalid JWKS_REFRESH_TTL: %w", err)
		}
		config.JWKSRefreshTTL = ttl
	}

	return nil
}

// applyOAuthConfigFromEnv populates OAuth-specific fields from environment variables.
func applyOAuthConfigFromEnv(config *Config) error {
	config.OAuthClientID = os.Getenv("OAUTH_CLIENT_ID")
	config.OAuthClientSecret = os.Getenv("OAUTH_CLIENT_SECRET")
	config.OAuthTokenURL = os.Getenv("OAUTH_TOKEN_URL")

	if config.OAuthClientID == "" {
		return ErrMissingOAuthClientID
	}
	if config.OAuthClientSecret == "" {
		return ErrMissingOAuthClientSecret
	}
	if config.OAuthTokenURL == "" {
		return ErrMissingOAuthTokenURL
	}

	scopes := os.Getenv("OAUTH_SCOPES")
	if scopes != "" {
		config.OAuthScopes = splitScopes(scopes)
	}

	config.OAuthIntrospectionURL = os.Getenv("OAUTH_INTROSPECTION_URL")
	return nil
}

// DefaultConfig returns default authentication configuration for local development.
// This uses JWKS mode pointing to a local Keycloak instance.
// Use this for testing when Keycloak is running in Tilt or Docker Compose.
func DefaultConfig() Config {
	return Config{
		Mode:           AuthModeJWKS,
		JWKSURL:        "http://localhost:18080/realms/meridian/protocol/openid-connect/certs",
		JWKSCacheTTL:   1 * time.Hour,
		JWKSRefreshTTL: 30 * time.Minute,
		HTTPClient:     defaultHTTPClient(),
	}
}

// NewAuthenticator creates an authentication interceptor from the configuration.
// This is the primary integration point - call this once at service startup.
//
// For JWKS mode, it creates a JWKSProvider and JWTValidatorWithJWKS, then wraps them in an interceptor.
// For OAuth mode, it creates an OAuth2Client for outbound authentication.
// For disabled mode, it returns nil (no authentication).
//
// The caller is responsible for calling Close() on cleanup.
func (c Config) NewAuthenticator(ctx context.Context) (*Interceptor, error) {
	switch c.Mode {
	case AuthModeJWKS:
		// Create JWKS provider
		jwksConfig := &JWKSProviderConfig{
			URL:        c.JWKSURL,
			Client:     c.HTTPClient,
			CacheTTL:   c.JWKSCacheTTL,
			RefreshTTL: c.JWKSRefreshTTL,
		}

		provider, err := NewJWKSProvider(ctx, jwksConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
		}

		// Create validator
		validator, err := NewJWTValidatorWithJWKS(provider)
		if err != nil {
			return nil, fmt.Errorf("failed to create JWT validator: %w", err)
		}

		// Create interceptor
		interceptorConfig := &InterceptorConfig{
			JWKSValidator: validator,
		}
		return NewAuthInterceptor(interceptorConfig)

	case AuthModeOAuth:
		// OAuth mode is for outbound authentication (service-to-service)
		// Not used for incoming request validation
		return nil, ErrOAuthModeNotSupported

	case AuthModeDisabled:
		// No authentication - returning nil validator is intentional
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no validator and no error

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedAuthMode, c.Mode)
	}
}

// NewOAuthClient creates an OAuth 2.0 client from the configuration.
// This is used for outbound service-to-service authentication.
// Only works when Mode is AuthModeOAuth.
func (c Config) NewOAuthClient() (*OAuth2Client, error) {
	if c.Mode != AuthModeOAuth {
		return nil, fmt.Errorf("%w, got %q", ErrInvalidModeForOAuthClient, c.Mode)
	}

	oauthConfig := &OAuth2Config{
		ClientID:     c.OAuthClientID,
		ClientSecret: c.OAuthClientSecret,
		TokenURL:     c.OAuthTokenURL,
		Scopes:       c.OAuthScopes,
		Client:       c.HTTPClient,
	}

	return NewOAuth2Client(oauthConfig)
}

// NewIntrospector creates an OAuth 2.0 token introspector from the configuration.
// This is used for validating tokens via OAuth introspection endpoint.
// Only works when Mode is AuthModeOAuth and OAuthIntrospectionURL is set.
func (c Config) NewIntrospector() (*OAuth2Introspector, error) {
	if c.Mode != AuthModeOAuth {
		return nil, fmt.Errorf("%w, got %q", ErrInvalidModeForIntrospector, c.Mode)
	}

	if c.OAuthIntrospectionURL == "" {
		return nil, ErrMissingIntrospectionURL
	}

	return NewOAuth2Introspector(
		c.OAuthIntrospectionURL,
		c.OAuthClientID,
		c.OAuthClientSecret,
		c.HTTPClient,
	)
}

// splitScopes splits a comma-separated scope string into a slice
func splitScopes(scopes string) []string {
	if scopes == "" {
		return nil
	}
	parts := strings.Split(scopes, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
