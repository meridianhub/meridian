package auth

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

var (
	// ErrInvalidToken is returned when the JWT token is invalid or malformed
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenExpired is returned when the JWT token has expired
	ErrTokenExpired = errors.New("token expired")
	// ErrInvalidSignature is returned when the token signature verification fails
	ErrInvalidSignature = errors.New("invalid signature")
	// ErrPublicKeyNil is returned when a nil public key is provided
	ErrPublicKeyNil = errors.New("public key cannot be nil")
	// ErrTokenStringEmpty is returned when an empty token string is provided
	ErrTokenStringEmpty = errors.New("token string cannot be empty")
	// ErrTenantClaimMissing is returned when the tenant ID claim is missing from the token
	ErrTenantClaimMissing = errors.New("x-tenant-id claim missing")
)

// Claims represents the JWT claims extracted from a validated token.
// It contains standard JWT claims plus custom claims for user identification and authorization.
// It also supports standard OIDC claims (email, name) for compatibility with external
// identity providers like Dex that issue standard tokens without custom Meridian claims.
type Claims struct {
	UserID string `json:"user_id"`
	// TenantID is the tenant identifier extracted from the x-tenant-id JWT claim.
	TenantID string   `json:"x-tenant-id"`
	Roles    []string `json:"roles"`
	Scopes   []string `json:"scopes"`
	// Groups is the standard OIDC groups claim, present in tokens from providers like Dex.
	// When Roles is empty, Groups is used as the effective roles via EffectiveRoles().
	Groups []string `json:"groups"`
	// Email is the standard OIDC email claim, present in tokens from providers like Dex.
	Email string `json:"email"`
	// Name is the standard OIDC name claim.
	Name string `json:"name"`
	jwt.RegisteredClaims
}

// JWTValidator validates JWT tokens using RS256 algorithm.
// It provides thread-safe token validation and claims extraction.
type JWTValidator struct {
	publicKey *rsa.PublicKey
}

// NewJWTValidator creates a new JWT validator with the specified RSA public key.
// The public key is used to verify token signatures using RS256 algorithm.
func NewJWTValidator(publicKey *rsa.PublicKey) (*JWTValidator, error) {
	if publicKey == nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", ErrPublicKeyNil)
	}

	return &JWTValidator{
		publicKey: publicKey,
	}, nil
}

// ValidateToken validates a JWT token string and returns the extracted claims.
// It verifies the token signature, expiration time, and claims structure.
// Returns an error if the token is invalid, expired, or has an invalid signature.
func (v *JWTValidator) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("failed to validate token: %w", ErrTokenStringEmpty)
	}

	// Parse and validate token
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method is RS256
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v: %w", token.Header["alg"], ErrInvalidSignature)
		}
		return v.publicKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("failed to validate token: %w", ErrTokenExpired)
		}
		if errors.Is(err, jwt.ErrSignatureInvalid) {
			return nil, fmt.Errorf("failed to validate token: %w", ErrInvalidSignature)
		}
		return nil, fmt.Errorf("failed to parse token: %w: %w", err, ErrInvalidToken)
	}

	// Extract claims
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("failed to extract claims: %w", ErrInvalidToken)
	}

	return claims, nil
}

// GetUserID extracts the user ID from the validated claims.
// Returns an empty string if the user ID claim is not present.
func (c *Claims) GetUserID() string {
	return c.UserID
}

// EffectiveUserID returns the best available user identifier.
// It prefers the custom UserID claim, falling back to the standard OIDC Subject claim.
// This enables compatibility with identity providers like Dex that use "sub" instead of "user_id".
func (c *Claims) EffectiveUserID() string {
	if c.UserID != "" {
		return c.UserID
	}
	return c.Subject
}

// GetTenantID extracts and validates the tenant ID from the claims.
// Returns ErrTenantClaimMissing if the x-tenant-id claim is absent.
// Returns tenant.ErrInvalidTenantID if the format is invalid.
func (c *Claims) GetTenantID() (tenant.TenantID, error) {
	if c.TenantID == "" {
		return "", ErrTenantClaimMissing
	}
	return tenant.NewTenantID(c.TenantID)
}

// HasTenantID returns true if the tenant ID claim is present.
// Use this for quick presence checks before calling GetTenantID().
func (c *Claims) HasTenantID() bool {
	return c.TenantID != ""
}

// EffectiveRoles returns the roles to use for authorization decisions.
// If the Roles claim is non-empty, it is returned. Otherwise, Groups is used as a fallback.
// This enables compatibility with identity providers like Dex that use "groups" instead of "roles".
// Returns a defensive copy to prevent external mutation.
func (c *Claims) EffectiveRoles() []string {
	source := c.Roles
	if len(source) == 0 {
		source = c.Groups
	}
	if len(source) == 0 {
		return []string{}
	}
	result := make([]string, len(source))
	copy(result, source)
	return result
}

// GetRoles extracts the effective roles from the validated claims.
// Returns a defensive copy to prevent external mutation.
// Returns an empty slice if no roles are present.
// Uses EffectiveRoles() to support groups-to-roles fallback.
func (c *Claims) GetRoles() []string {
	return c.EffectiveRoles()
}

// GetScopes extracts the scopes from the validated claims.
// Returns a defensive copy to prevent external mutation.
// Returns an empty slice if no scopes are present.
func (c *Claims) GetScopes() []string {
	if c.Scopes == nil {
		return []string{}
	}
	// Return defensive copy to maintain immutability
	scopes := make([]string, len(c.Scopes))
	copy(scopes, c.Scopes)
	return scopes
}

// HasRole checks if the claims contain a specific role.
// Uses EffectiveRoles() to support groups-to-roles fallback.
func (c *Claims) HasRole(role string) bool {
	for _, r := range c.EffectiveRoles() {
		if r == role {
			return true
		}
	}
	return false
}

// HasScope checks if the claims contain a specific scope.
func (c *Claims) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// IsExpired checks if the token has expired.
func (c *Claims) IsExpired() bool {
	if c.ExpiresAt == nil {
		return false
	}
	return c.ExpiresAt.Before(time.Now())
}
