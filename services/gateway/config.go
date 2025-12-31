// Package gateway provides the multi-tenant API gateway for routing requests
// to backend services based on tenant identification from subdomain or header.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
)

// Config holds the configuration for the gateway service.
type Config struct {
	// Port is the HTTP port to listen on (default 8080).
	Port int

	// BaseDomain is the base domain for subdomain-based tenant identification
	// (e.g., "api.meridianhub.cloud").
	BaseDomain string

	// LocalDevMode allows using X-Tenant-Slug header for tenant identification
	// in development environments (default false).
	LocalDevMode bool

	// DatabaseURL is the connection string for tenant lookups.
	DatabaseURL string

	// RedisURL is the optional Redis URL for caching (use in-memory if empty).
	RedisURL string

	// Backends is the list of backend routes for request proxying.
	Backends []BackendRoute

	// Auth contains the authentication configuration.
	Auth AuthConfig
}

// AuthConfig holds the authentication configuration for the gateway.
type AuthConfig struct {
	// Enabled controls whether authentication is required for API routes.
	// When false, all requests bypass authentication (useful for testing).
	Enabled bool

	// JWKSURL is the URL to fetch JSON Web Key Set for JWT validation.
	// Required when Enabled is true.
	JWKSURL string

	// JWKSCacheTTL is how long to cache JWKS keys (default: 24h).
	JWKSCacheTTL time.Duration

	// JWKSRefreshTTL is the background refresh interval for JWKS keys (default: 1h).
	JWKSRefreshTTL time.Duration

	// Issuer is the expected JWT issuer (iss claim) for validation.
	// Optional - if empty, issuer validation is skipped.
	Issuer string

	// Audience is the expected JWT audience (aud claim) for validation.
	// Optional - if empty, audience validation is skipped.
	Audience string

	// APIKeys maps API key strings to their identity names.
	// Used for service-to-service authentication as an alternative to JWT.
	APIKeys map[string]string

	// RateLimitPerSecond is the number of requests allowed per second per API key.
	// Defaults to 100 if not set.
	RateLimitPerSecond float64

	// RateLimitBurst is the maximum burst size for rate limiting.
	// Defaults to 200 if not set.
	RateLimitBurst int
}

// BackendRoute defines a mapping from a URL prefix to a backend service.
type BackendRoute struct {
	// Prefix is the URL path prefix to match (e.g., "/v1/party").
	Prefix string `json:"prefix"`

	// Target is the backend service address (e.g., "party-service:50051").
	Target string `json:"target"`
}

// Configuration errors.
var (
	// ErrBaseDomainRequired is returned when BASE_DOMAIN is not set.
	ErrBaseDomainRequired = errors.New("BASE_DOMAIN is required")

	// ErrDatabaseURLRequired is returned when DATABASE_URL is not set.
	ErrDatabaseURLRequired = errors.New("DATABASE_URL is required")

	// ErrInvalidPort is returned when PORT is not a valid integer.
	ErrInvalidPort = errors.New("PORT must be a valid integer between 1 and 65535")

	// ErrInvalidBackendsJSON is returned when BACKENDS contains invalid JSON.
	ErrInvalidBackendsJSON = errors.New("BACKENDS must be valid JSON array")

	// ErrInvalidBackendRoute is returned when a backend route has empty prefix or target.
	ErrInvalidBackendRoute = errors.New("backend route must have non-empty prefix and target")

	// ErrLocalDevModeInProduction is returned when LOCAL_DEV_MODE is enabled in a production namespace.
	ErrLocalDevModeInProduction = errors.New("LOCAL_DEV_MODE cannot be enabled in production namespace")

	// ErrJWKSURLRequired is returned when AUTH_ENABLED is true but JWKS_URL is not set.
	ErrJWKSURLRequired = errors.New("JWKS_URL is required when AUTH_ENABLED is true")
)

// LoadConfig loads configuration from environment variables.
// It validates required fields and returns an error if validation fails.
func LoadConfig() (*Config, error) {
	config := &Config{
		Port:         env.GetEnvAsInt("PORT", 8080),
		BaseDomain:   os.Getenv("BASE_DOMAIN"),
		LocalDevMode: env.GetEnvAsBool("LOCAL_DEV_MODE", false),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		RedisURL:     os.Getenv("REDIS_URL"),
	}

	// Parse backend routes from JSON
	backendsJSON := os.Getenv("BACKENDS")
	if backendsJSON != "" {
		var backends []BackendRoute
		if err := json.Unmarshal([]byte(backendsJSON), &backends); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidBackendsJSON, err)
		}
		config.Backends = backends
	}

	// Load auth configuration
	config.Auth = loadAuthConfig()

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// loadAuthConfig loads authentication configuration from environment variables.
//
// Environment variables:
//   - AUTH_ENABLED: Enable authentication (default: false)
//   - JWKS_URL: JWKS endpoint URL for JWT validation
//   - JWKS_CACHE_TTL: Cache duration for JWKS keys (default: 24h)
//   - JWKS_REFRESH_TTL: Background refresh interval (default: 1h)
//   - JWT_ISSUER: Expected JWT issuer (optional)
//   - JWT_AUDIENCE: Expected JWT audience (optional)
//   - API_KEYS: Comma-separated list of "key:identity" pairs
//   - API_KEY_RATE_LIMIT_PER_SECOND: Requests per second per key (default: 100)
//   - API_KEY_RATE_LIMIT_BURST: Burst size for rate limiting (default: 200)
func loadAuthConfig() AuthConfig {
	config := AuthConfig{
		Enabled:            env.GetEnvAsBool("AUTH_ENABLED", false),
		JWKSURL:            os.Getenv("JWKS_URL"),
		Issuer:             os.Getenv("JWT_ISSUER"),
		Audience:           os.Getenv("JWT_AUDIENCE"),
		RateLimitPerSecond: 100,
		RateLimitBurst:     200,
	}

	// Parse JWKS cache TTL
	if ttl := os.Getenv("JWKS_CACHE_TTL"); ttl != "" {
		if d, err := time.ParseDuration(ttl); err == nil {
			config.JWKSCacheTTL = d
		}
	}
	if config.JWKSCacheTTL == 0 {
		config.JWKSCacheTTL = 24 * time.Hour
	}

	// Parse JWKS refresh TTL
	if ttl := os.Getenv("JWKS_REFRESH_TTL"); ttl != "" {
		if d, err := time.ParseDuration(ttl); err == nil {
			config.JWKSRefreshTTL = d
		}
	}
	if config.JWKSRefreshTTL == 0 {
		config.JWKSRefreshTTL = 1 * time.Hour
	}

	// Parse API keys (format: "key1:identity1,key2:identity2")
	if apiKeysEnv := os.Getenv("API_KEYS"); apiKeysEnv != "" {
		config.APIKeys = parseAPIKeysEnv(apiKeysEnv)
	}

	// Parse rate limit per second
	if rps := os.Getenv("API_KEY_RATE_LIMIT_PER_SECOND"); rps != "" {
		if v := env.GetEnvAsInt("API_KEY_RATE_LIMIT_PER_SECOND", 100); v > 0 {
			config.RateLimitPerSecond = float64(v)
		}
	}

	// Parse rate limit burst
	if burst := os.Getenv("API_KEY_RATE_LIMIT_BURST"); burst != "" {
		if v := env.GetEnvAsInt("API_KEY_RATE_LIMIT_BURST", 200); v > 0 {
			config.RateLimitBurst = v
		}
	}

	return config
}

// parseAPIKeysEnv parses a comma-separated list of "key:identity" pairs.
func parseAPIKeysEnv(env string) map[string]string {
	keys := make(map[string]string)
	pairs := strings.Split(env, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			identity := strings.TrimSpace(parts[1])
			if key != "" && identity != "" {
				keys[key] = identity
			}
		}
	}
	return keys
}

// Validate checks that all required configuration values are set and valid.
func (c *Config) Validate() error {
	if c.BaseDomain == "" {
		return ErrBaseDomainRequired
	}

	if c.DatabaseURL == "" {
		return ErrDatabaseURLRequired
	}

	if c.Port < 1 || c.Port > 65535 {
		return ErrInvalidPort
	}

	// Validate backend routes
	for _, backend := range c.Backends {
		if backend.Prefix == "" || backend.Target == "" {
			return ErrInvalidBackendRoute
		}
	}

	// Validate auth configuration
	if c.Auth.Enabled && c.Auth.JWKSURL == "" {
		return ErrJWKSURLRequired
	}

	return nil
}

// ValidateForNamespace checks if the configuration is safe for the given namespace.
// Returns an error if LOCAL_DEV_MODE is enabled in a production namespace.
func (c *Config) ValidateForNamespace(namespace string) error {
	if c.LocalDevMode && strings.HasPrefix(namespace, "prod") {
		return fmt.Errorf("%w: %s", ErrLocalDevModeInProduction, namespace)
	}
	return nil
}
