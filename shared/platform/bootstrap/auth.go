package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"
)

// ErrAuthMissingJWKSURL is returned when auth is enabled but JWKS URL is not configured.
var ErrAuthMissingJWKSURL = errors.New("AUTH_JWKS_URL is required when authentication is enabled")

// AuthConfig holds configuration for authentication interceptor initialization.
type AuthConfig struct {
	// Enabled controls whether authentication is active.
	// When false, NewAuthInterceptor returns nil (no-op).
	Enabled bool

	// JWKSURL is the JWKS endpoint URL for JWT validation.
	// Required when Enabled is true.
	JWKSURL string

	// RefreshTTL is the background refresh interval for JWKS keys.
	RefreshTTL time.Duration

	// HTTPTimeout is the timeout for HTTP requests to the JWKS endpoint.
	HTTPTimeout time.Duration

	// BypassMethods are gRPC methods that should skip authentication.
	// Typically includes health checks and reflection endpoints.
	BypassMethods []string

	// Logger is used for auth lifecycle events.
	Logger *slog.Logger
}

// DefaultAuthConfig returns an AuthConfig populated from environment variables.
//
// Environment variables:
//   - AUTH_ENABLED: Enable authentication (default: "true")
//   - AUTH_JWKS_URL: JWKS endpoint URL (required when enabled)
//   - AUTH_JWKS_REFRESH_TTL: Background refresh interval (default: "30m")
//   - AUTH_HTTP_TIMEOUT: HTTP timeout for JWKS requests (default: "30s")
//
// The BypassMethods field is populated with DefaultBypassMethods().
func DefaultAuthConfig(logger *slog.Logger) AuthConfig {
	return AuthConfig{
		Enabled:       env.GetEnvAsBool("AUTH_ENABLED", true),
		JWKSURL:       env.GetEnvOrDefault("AUTH_JWKS_URL", ""),
		RefreshTTL:    env.GetEnvAsDuration("AUTH_JWKS_REFRESH_TTL", 30*time.Minute),
		HTTPTimeout:   env.GetEnvAsDuration("AUTH_HTTP_TIMEOUT", 30*time.Second),
		BypassMethods: DefaultBypassMethods(),
		Logger:        logger,
	}
}

// DefaultBypassMethods returns the standard list of gRPC methods that should
// bypass authentication. This includes health checks and gRPC reflection.
func DefaultBypassMethods() []string {
	return []string{
		// gRPC health check service
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch",
		// gRPC reflection service (for development tools like grpcurl)
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
		"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
	}
}

// NewAuthInterceptor creates an authentication interceptor from the configuration.
// If Enabled is false, it returns nil with a warning log (no-op auth).
// If Enabled is true but JWKSURL is empty, it returns an error.
//
// The interceptor uses JWKS-based JWT validation with automatic key rotation.
func NewAuthInterceptor(ctx context.Context, cfg AuthConfig) (*auth.Interceptor, error) {
	if !cfg.Enabled {
		if cfg.Logger != nil {
			cfg.Logger.Warn("authentication is disabled",
				"reason", "AUTH_ENABLED=false")
		}
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no interceptor and no error
	}

	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("failed to create auth interceptor: %w", ErrAuthMissingJWKSURL)
	}

	// Create HTTP client with configured timeout
	httpClient := &http.Client{
		Timeout: cfg.HTTPTimeout,
	}

	// Create JWKS provider
	jwksConfig := &auth.JWKSProviderConfig{
		URL:        cfg.JWKSURL,
		Client:     httpClient,
		CacheTTL:   24 * time.Hour, // Default cache TTL
		RefreshTTL: cfg.RefreshTTL,
	}

	provider, err := auth.NewJWKSProvider(ctx, jwksConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator
	validator, err := auth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		// Clean up provider on validator creation failure
		_ = provider.Close()
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	// Create interceptor
	interceptorConfig := &auth.InterceptorConfig{
		JWKSValidator: validator,
		BypassMethods: cfg.BypassMethods,
		Logger:        cfg.Logger,
	}

	interceptor, err := auth.NewAuthInterceptor(interceptorConfig)
	if err != nil {
		// Clean up resources on failure
		_ = provider.Close()
		return nil, fmt.Errorf("failed to create auth interceptor: %w", err)
	}

	if cfg.Logger != nil {
		cfg.Logger.Info("auth interceptor initialized",
			"jwks_url", cfg.JWKSURL,
			"refresh_ttl", cfg.RefreshTTL,
			"bypass_methods_count", len(cfg.BypassMethods))
	}

	return interceptor, nil
}
