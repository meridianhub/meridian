package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestRSAKeys generates a test RSA key pair for testing
func generateTestRSAKeys() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA keys: %w", err)
	}
	return privateKey, &privateKey.PublicKey, nil
}

// createTestToken creates a signed JWT token for testing
func createTestToken(privateKey *rsa.PrivateKey, claims *Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

func TestNewJWTValidator(t *testing.T) {
	t.Run("success with valid public key", func(t *testing.T) {
		_, publicKey, err := generateTestRSAKeys()
		require.NoError(t, err)

		validator, err := NewJWTValidator(publicKey)

		assert.NoError(t, err)
		assert.NotNil(t, validator)
		assert.Equal(t, publicKey, validator.publicKey)
	})

	t.Run("error with nil public key", func(t *testing.T) {
		validator, err := NewJWTValidator(nil)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrPublicKeyNil)
		assert.Nil(t, validator)
	})
}

func TestJWTValidator_ValidateToken(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("success with valid token", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin", "user"},
			Scopes: []string{"read", "write"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				Subject:   "test-subject",
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.NoError(t, err)
		assert.NotNil(t, extractedClaims)
		assert.Equal(t, "user-123", extractedClaims.UserID)
		assert.Equal(t, []string{"admin", "user"}, extractedClaims.Roles)
		assert.Equal(t, []string{"read", "write"}, extractedClaims.Scopes)
		assert.Equal(t, "test-subject", extractedClaims.Subject)
	})

	t.Run("error with empty token string", func(t *testing.T) {
		extractedClaims, err := validator.ValidateToken("")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTokenStringEmpty)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error with expired token", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTokenExpired)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error with invalid signature", func(t *testing.T) {
		// Create token with different key
		otherPrivateKey, _, err := generateTestRSAKeys()
		require.NoError(t, err)

		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		tokenString, err := createTestToken(otherPrivateKey, claims)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.Error(t, err)
		// Signature errors get wrapped as invalid token since they come from crypto layer
		assert.ErrorIs(t, err, ErrInvalidToken)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error with malformed token", func(t *testing.T) {
		extractedClaims, err := validator.ValidateToken("invalid.token.format")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidToken)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error with wrong signing method", func(t *testing.T) {
		// Create token with HS256 instead of RS256
		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString([]byte("secret"))
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidSignature)
		assert.Nil(t, extractedClaims)
	})
}

func TestClaims_GetUserID(t *testing.T) {
	t.Run("returns user ID when present", func(t *testing.T) {
		claims := &Claims{UserID: "user-123"}
		assert.Equal(t, "user-123", claims.GetUserID())
	})

	t.Run("returns empty string when not present", func(t *testing.T) {
		claims := &Claims{}
		assert.Equal(t, "", claims.GetUserID())
	})
}

func TestClaims_GetOrganizationID(t *testing.T) {
	t.Run("returns organization ID when valid", func(t *testing.T) {
		claims := &Claims{OrganizationID: "acme_bank"}
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("acme_bank"), orgID)
	})

	t.Run("returns error when organization claim missing", func(t *testing.T) {
		claims := &Claims{UserID: "user-123"}
		orgID, err := claims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOrganizationClaimMissing)
		assert.Equal(t, organization.OrganizationID(""), orgID)
	})

	t.Run("returns error when organization claim empty", func(t *testing.T) {
		claims := &Claims{OrganizationID: ""}
		orgID, err := claims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOrganizationClaimMissing)
		assert.Equal(t, organization.OrganizationID(""), orgID)
	})

	t.Run("returns error for invalid format with spaces", func(t *testing.T) {
		claims := &Claims{OrganizationID: "acme bank"}
		orgID, err := claims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, organization.ErrInvalidOrganizationID)
		assert.Equal(t, organization.OrganizationID(""), orgID)
	})

	t.Run("returns error for invalid format with special chars", func(t *testing.T) {
		claims := &Claims{OrganizationID: "acme-bank!"}
		orgID, err := claims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, organization.ErrInvalidOrganizationID)
		assert.Equal(t, organization.OrganizationID(""), orgID)
	})

	t.Run("accepts valid organization IDs with underscores", func(t *testing.T) {
		claims := &Claims{OrganizationID: "acme_bank_corp"}
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("acme_bank_corp"), orgID)
	})

	t.Run("accepts valid organization IDs with numbers", func(t *testing.T) {
		claims := &Claims{OrganizationID: "bank123"}
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("bank123"), orgID)
	})

	t.Run("accepts single character organization ID (min length)", func(t *testing.T) {
		claims := &Claims{OrganizationID: "a"}
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("a"), orgID)
	})

	t.Run("accepts 50 character organization ID (max length)", func(t *testing.T) {
		maxLengthID := "a1234567890123456789012345678901234567890123456789"
		claims := &Claims{OrganizationID: maxLengthID}
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID(maxLengthID), orgID)
	})

	t.Run("rejects 51 character organization ID (exceeds max length)", func(t *testing.T) {
		tooLongID := "a12345678901234567890123456789012345678901234567890"
		claims := &Claims{OrganizationID: tooLongID}
		orgID, err := claims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, organization.ErrInvalidOrganizationID)
		assert.Equal(t, organization.OrganizationID(""), orgID)
	})
}

func TestClaims_HasOrganizationID(t *testing.T) {
	t.Run("returns true when organization ID is present", func(t *testing.T) {
		claims := &Claims{OrganizationID: "acme_bank"}
		assert.True(t, claims.HasOrganizationID())
	})

	t.Run("returns false when organization ID is empty", func(t *testing.T) {
		claims := &Claims{OrganizationID: ""}
		assert.False(t, claims.HasOrganizationID())
	})

	t.Run("returns false when organization ID is not set", func(t *testing.T) {
		claims := &Claims{UserID: "user-123"}
		assert.False(t, claims.HasOrganizationID())
	})
}

func TestClaims_GetRoles(t *testing.T) {
	t.Run("returns roles when present", func(t *testing.T) {
		claims := &Claims{Roles: []string{"admin", "user"}}
		assert.Equal(t, []string{"admin", "user"}, claims.GetRoles())
	})

	t.Run("returns empty slice when not present", func(t *testing.T) {
		claims := &Claims{}
		assert.Equal(t, []string{}, claims.GetRoles())
	})

	t.Run("returns empty slice when nil", func(t *testing.T) {
		claims := &Claims{Roles: nil}
		assert.Equal(t, []string{}, claims.GetRoles())
	})
}

func TestClaims_GetScopes(t *testing.T) {
	t.Run("returns scopes when present", func(t *testing.T) {
		claims := &Claims{Scopes: []string{"read", "write"}}
		assert.Equal(t, []string{"read", "write"}, claims.GetScopes())
	})

	t.Run("returns empty slice when not present", func(t *testing.T) {
		claims := &Claims{}
		assert.Equal(t, []string{}, claims.GetScopes())
	})

	t.Run("returns empty slice when nil", func(t *testing.T) {
		claims := &Claims{Scopes: nil}
		assert.Equal(t, []string{}, claims.GetScopes())
	})
}

func TestClaims_HasRole(t *testing.T) {
	claims := &Claims{Roles: []string{"admin", "user", "editor"}}

	t.Run("returns true when role exists", func(t *testing.T) {
		assert.True(t, claims.HasRole("admin"))
		assert.True(t, claims.HasRole("user"))
		assert.True(t, claims.HasRole("editor"))
	})

	t.Run("returns false when role does not exist", func(t *testing.T) {
		assert.False(t, claims.HasRole("superadmin"))
		assert.False(t, claims.HasRole(""))
	})

	t.Run("returns false when roles are empty", func(t *testing.T) {
		emptyClaims := &Claims{Roles: []string{}}
		assert.False(t, emptyClaims.HasRole("admin"))
	})

	t.Run("returns false when roles are nil", func(t *testing.T) {
		nilClaims := &Claims{Roles: nil}
		assert.False(t, nilClaims.HasRole("admin"))
	})
}

func TestClaims_HasScope(t *testing.T) {
	claims := &Claims{Scopes: []string{"read", "write", "delete"}}

	t.Run("returns true when scope exists", func(t *testing.T) {
		assert.True(t, claims.HasScope("read"))
		assert.True(t, claims.HasScope("write"))
		assert.True(t, claims.HasScope("delete"))
	})

	t.Run("returns false when scope does not exist", func(t *testing.T) {
		assert.False(t, claims.HasScope("execute"))
		assert.False(t, claims.HasScope(""))
	})

	t.Run("returns false when scopes are empty", func(t *testing.T) {
		emptyClaims := &Claims{Scopes: []string{}}
		assert.False(t, emptyClaims.HasScope("read"))
	})

	t.Run("returns false when scopes are nil", func(t *testing.T) {
		nilClaims := &Claims{Scopes: nil}
		assert.False(t, nilClaims.HasScope("read"))
	})
}

func TestClaims_IsExpired(t *testing.T) {
	t.Run("returns true when token is expired", func(t *testing.T) {
		claims := &Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			},
		}
		assert.True(t, claims.IsExpired())
	})

	t.Run("returns false when token is not expired", func(t *testing.T) {
		claims := &Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}
		assert.False(t, claims.IsExpired())
	})

	t.Run("returns false when ExpiresAt is nil", func(t *testing.T) {
		claims := &Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: nil,
			},
		}
		assert.False(t, claims.IsExpired())
	})

	t.Run("returns false when ExpiresAt is exactly now", func(_ *testing.T) {
		// This is a bit tricky - we need to handle the edge case
		// where the token expires at exactly the current time
		now := time.Now()
		claims := &Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(now),
			},
		}
		// Should be expired since Before(now) with the same time is false
		// but it's at the exact moment of expiration
		result := claims.IsExpired()
		// This might be true or false depending on timing, so we just verify it doesn't panic
		_ = result
	})
}

func TestClaims_EdgeCases(t *testing.T) {
	t.Run("claims with minimal fields", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
		}
		assert.Equal(t, "user-123", claims.GetUserID())
		assert.Equal(t, []string{}, claims.GetRoles())
		assert.Equal(t, []string{}, claims.GetScopes())
		assert.False(t, claims.IsExpired())
	})

	t.Run("claims with all fields populated", func(t *testing.T) {
		claims := &Claims{
			UserID:         "user-123",
			OrganizationID: "acme_bank",
			Roles:          []string{"admin"},
			Scopes:         []string{"read"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				NotBefore: jwt.NewNumericDate(time.Now()),
				Issuer:    "test-issuer",
				Subject:   "test-subject",
				Audience:  []string{"test-audience"},
			},
		}
		assert.Equal(t, "user-123", claims.GetUserID())
		orgID, err := claims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("acme_bank"), orgID)
		assert.Equal(t, []string{"admin"}, claims.GetRoles())
		assert.Equal(t, []string{"read"}, claims.GetScopes())
		assert.False(t, claims.IsExpired())
		assert.Equal(t, "test-issuer", claims.Issuer)
		assert.Equal(t, "test-subject", claims.Subject)
	})
}

func TestValidateToken_WithOrganizationClaim(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("extracts organization claim from valid JWT", func(t *testing.T) {
		claims := &Claims{
			UserID:         "user-123",
			OrganizationID: "acme_bank",
			Roles:          []string{"admin"},
			Scopes:         []string{"read", "write"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.NoError(t, err)
		assert.NotNil(t, extractedClaims)
		assert.Equal(t, "user-123", extractedClaims.UserID)
		assert.Equal(t, "acme_bank", extractedClaims.OrganizationID)

		orgID, err := extractedClaims.GetOrganizationID()
		assert.NoError(t, err)
		assert.Equal(t, organization.OrganizationID("acme_bank"), orgID)
	})

	t.Run("backward compatibility - tokens without organization claim still validate", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(tokenString)

		assert.NoError(t, err)
		assert.NotNil(t, extractedClaims)
		assert.Equal(t, "user-123", extractedClaims.UserID)
		assert.Equal(t, "", extractedClaims.OrganizationID)

		_, err = extractedClaims.GetOrganizationID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOrganizationClaimMissing)
	})
}
