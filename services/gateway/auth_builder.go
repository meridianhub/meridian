package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/gateway/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// BuildAuthMiddleware creates a CombinedAuthMiddleware from the gateway AuthConfig.
// The caller should only invoke this when config.Enabled is true.
//
// The caller is responsible for calling Close() on the returned middleware
// during shutdown to release JWKS provider resources.
func BuildAuthMiddleware(config AuthConfig, logger *slog.Logger) (*auth.CombinedAuthMiddleware, error) {
	// Create JWKS provider for JWT validation
	provider, err := platformauth.NewJWKSProvider(context.Background(), &platformauth.JWKSProviderConfig{
		URL:        config.JWKSURL,
		Client:     &http.Client{Timeout: 30 * time.Second},
		CacheTTL:   config.JWKSCacheTTL,
		RefreshTTL: config.JWKSRefreshTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator
	jwtValidator, err := platformauth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	// Wrap in context adapter for the gateway middleware interface
	validatorAdapter := auth.NewJWTValidatorWithContext(jwtValidator)

	// Build API key config
	apiKeyConfig := auth.APIKeyConfig{
		APIKeys:            config.APIKeys,
		RateLimitPerSecond: config.RateLimitPerSecond,
		RateLimitBurst:     config.RateLimitBurst,
	}

	// Create combined middleware
	middleware, err := auth.NewCombinedAuthMiddleware(auth.CombinedAuthConfig{
		JWTValidator: validatorAdapter,
		JWTConfig: auth.JWTMiddlewareConfig{
			DefaultTenantID: config.DefaultTenantID,
			DefaultRoles:    config.DefaultRoles,
		},
		APIKeyConfig: apiKeyConfig,
		Logger:       logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	logger.Info("auth middleware initialized",
		"jwks_url", config.JWKSURL,
		"api_keys_configured", len(config.APIKeys) > 0,
		"default_tenant_id", config.DefaultTenantID,
		"default_roles_configured", len(config.DefaultRoles) > 0)

	return middleware, nil
}
