package auth

import (
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// CompositeJWTValidator tries multiple validators in order.
// This supports tokens signed by different issuers (e.g., Meridian BFF + Dex SSO).
// The first validator that succeeds wins. If all fail, the last error is returned.
type CompositeJWTValidator struct {
	validators []JWTValidator
}

// NewCompositeJWTValidator creates a validator that tries each validator in order.
func NewCompositeJWTValidator(validators ...JWTValidator) *CompositeJWTValidator {
	return &CompositeJWTValidator{validators: validators}
}

// ValidateToken tries each validator in order. Returns the first successful result.
func (c *CompositeJWTValidator) ValidateToken(tokenString string) (*platformauth.Claims, error) {
	var lastErr error
	for _, v := range c.validators {
		claims, err := v.ValidateToken(tokenString)
		if err == nil {
			return claims, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
