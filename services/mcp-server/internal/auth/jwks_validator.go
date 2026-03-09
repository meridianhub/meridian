// Package auth provides API key extraction and gRPC client authentication
// for the MCP server, as well as JWKS-based bearer token validation for
// production OAuth 2.1 deployments.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

const (
	// EnvJWKSURL is the environment variable for the JWKS endpoint URL.
	// When set, JWKS-based bearer token validation is used instead of the passthrough.
	EnvJWKSURL = "MCP_JWKS_URL"

	jwksCacheTTL    = 24 * time.Hour
	jwksRefreshTTL  = 1 * time.Hour
	jwksHTTPTimeout = 10 * time.Second
)

// ErrJWKSURLEmpty is returned when an empty JWKS URL is provided to NewJWKSBearerValidator.
var ErrJWKSURLEmpty = errors.New("JWKS URL must not be empty")

// JWKSBearerValidator validates Bearer tokens using public keys fetched from
// a JWKS endpoint. It wraps the shared JWTValidatorWithJWKS to satisfy the
// BearerValidator interface used by BearerMiddleware.
type JWKSBearerValidator struct {
	validator *platformauth.JWTValidatorWithJWKS
}

// NewJWKSBearerValidator creates a JWKSBearerValidator that fetches public keys
// from the given JWKS URL. It performs an initial key fetch at construction time
// and starts a background refresh goroutine.
//
// The caller is responsible for calling Close() when the validator is no longer needed.
func NewJWKSBearerValidator(ctx context.Context, jwksURL string) (*JWKSBearerValidator, error) {
	if jwksURL == "" {
		return nil, ErrJWKSURLEmpty
	}

	provider, err := platformauth.NewJWKSProvider(ctx, &platformauth.JWKSProviderConfig{
		URL:        jwksURL,
		Client:     &http.Client{Timeout: jwksHTTPTimeout},
		CacheTTL:   jwksCacheTTL,
		RefreshTTL: jwksRefreshTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("create JWKS provider: %w", err)
	}

	jwksValidator, err := platformauth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		_ = provider.Close()
		return nil, fmt.Errorf("create JWKS validator: %w", err)
	}

	return &JWKSBearerValidator{validator: jwksValidator}, nil
}

// ValidateBearer validates the raw Bearer token string. It satisfies the
// BearerValidator interface expected by BearerMiddleware.
func (v *JWKSBearerValidator) ValidateBearer(token string) error {
	if _, err := v.validator.ValidateToken(context.Background(), token); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidBearerToken, err)
	}
	return nil
}

// ValidateBearerWithTenant validates the token and returns the tenant ID claim.
// It satisfies the ClaimsBearerValidator interface for subdomain validation.
func (v *JWKSBearerValidator) ValidateBearerWithTenant(token string) (string, error) {
	claims, err := v.validator.ValidateToken(context.Background(), token)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidBearerToken, err)
	}
	return claims.TenantID, nil
}

// Close releases resources held by the underlying JWKS provider, stopping
// the background key-refresh goroutine.
func (v *JWKSBearerValidator) Close() error {
	return v.validator.Close()
}
