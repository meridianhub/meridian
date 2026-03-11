package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// BuildAuthMiddleware creates a CombinedAuthMiddleware from the gateway AuthConfig.
// The caller should only invoke this when config.Enabled is true.
//
// If a local JWTSigner is provided, a composite validator is built that trusts both
// the local signer's key (for Meridian-issued BFF tokens) and the remote JWKS endpoint
// (for Dex SSO tokens). The local signer is tried first since most tokens will be
// Meridian-issued in the BFF flow.
//
// The caller is responsible for calling Close() on the returned middleware
// during shutdown to release JWKS provider resources.
func BuildAuthMiddleware(config AuthConfig, logger *slog.Logger, localSigners ...*platformauth.JWTSigner) (*auth.CombinedAuthMiddleware, error) {
	// Create JWKS provider for JWT validation (Dex or external IdP)
	provider, err := platformauth.NewJWKSProvider(context.Background(), &platformauth.JWKSProviderConfig{
		URL:        config.JWKSURL,
		Client:     &http.Client{Timeout: 30 * time.Second},
		CacheTTL:   config.JWKSCacheTTL,
		RefreshTTL: config.JWKSRefreshTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator for remote JWKS (Dex)
	jwksValidator, err := platformauth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}
	dexValidator := auth.NewJWTValidatorWithContext(jwksValidator)

	// Build the final JWT validator. If a local signer is provided,
	// create a composite that tries the local key first (faster, no network).
	var jwtValidator auth.JWTValidator = dexValidator
	if len(localSigners) > 0 && localSigners[0] != nil {
		localValidator, localErr := platformauth.NewJWTValidator(localSigners[0].PublicKey())
		if localErr != nil {
			return nil, fmt.Errorf("failed to create local JWT validator: %w", localErr)
		}
		jwtValidator = auth.NewCompositeJWTValidator(localValidator, dexValidator)
		logger.Info("auth middleware: composite validator (Meridian + Dex JWKS)")
	}

	// Build API key config
	apiKeyConfig := auth.APIKeyConfig{
		APIKeys:            config.APIKeys,
		RateLimitPerSecond: config.RateLimitPerSecond,
		RateLimitBurst:     config.RateLimitBurst,
	}

	// Create combined middleware
	middleware, err := auth.NewCombinedAuthMiddleware(auth.CombinedAuthConfig{
		JWTValidator: jwtValidator,
		APIKeyConfig: apiKeyConfig,
		Logger:       logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	logger.Info("auth middleware initialized",
		"jwks_url", config.JWKSURL,
		"api_keys_configured", len(config.APIKeys) > 0)

	return middleware, nil
}
