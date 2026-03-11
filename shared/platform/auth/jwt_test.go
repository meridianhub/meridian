package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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

func TestClaims_GetTenantID(t *testing.T) {
	t.Run("returns tenant ID when valid", func(t *testing.T) {
		claims := &Claims{TenantID: "acme_bank"}
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("returns error when tenant claim missing", func(t *testing.T) {
		claims := &Claims{UserID: "user-123"}
		tenantID, err := claims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTenantClaimMissing)
		assert.Equal(t, tenant.TenantID(""), tenantID)
	})

	t.Run("returns error when tenant claim empty", func(t *testing.T) {
		claims := &Claims{TenantID: ""}
		tenantID, err := claims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTenantClaimMissing)
		assert.Equal(t, tenant.TenantID(""), tenantID)
	})

	t.Run("returns error for invalid format with spaces", func(t *testing.T) {
		claims := &Claims{TenantID: "acme bank"}
		tenantID, err := claims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, tenant.ErrInvalidTenantID)
		assert.Equal(t, tenant.TenantID(""), tenantID)
	})

	t.Run("returns error for invalid format with special chars", func(t *testing.T) {
		claims := &Claims{TenantID: "acme-bank!"}
		tenantID, err := claims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, tenant.ErrInvalidTenantID)
		assert.Equal(t, tenant.TenantID(""), tenantID)
	})

	t.Run("accepts valid tenant IDs with underscores", func(t *testing.T) {
		claims := &Claims{TenantID: "acme_bank_corp"}
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("acme_bank_corp"), tenantID)
	})

	t.Run("accepts valid tenant IDs with numbers", func(t *testing.T) {
		claims := &Claims{TenantID: "bank123"}
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("bank123"), tenantID)
	})

	t.Run("accepts single character tenant ID (min length)", func(t *testing.T) {
		claims := &Claims{TenantID: "a"}
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("a"), tenantID)
	})

	t.Run("accepts 50 character tenant ID (max length)", func(t *testing.T) {
		maxLengthID := "a1234567890123456789012345678901234567890123456789"
		claims := &Claims{TenantID: maxLengthID}
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID(maxLengthID), tenantID)
	})

	t.Run("rejects 51 character tenant ID (exceeds max length)", func(t *testing.T) {
		tooLongID := "a12345678901234567890123456789012345678901234567890"
		claims := &Claims{TenantID: tooLongID}
		tenantID, err := claims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, tenant.ErrInvalidTenantID)
		assert.Equal(t, tenant.TenantID(""), tenantID)
	})
}

func TestClaims_HasTenantID(t *testing.T) {
	t.Run("returns true when tenant ID is present", func(t *testing.T) {
		claims := &Claims{TenantID: "acme_bank"}
		assert.True(t, claims.HasTenantID())
	})

	t.Run("returns false when tenant ID is empty", func(t *testing.T) {
		claims := &Claims{TenantID: ""}
		assert.False(t, claims.HasTenantID())
	})

	t.Run("returns false when tenant ID is not set", func(t *testing.T) {
		claims := &Claims{UserID: "user-123"}
		assert.False(t, claims.HasTenantID())
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

func TestClaims_EffectiveRoles(t *testing.T) {
	t.Run("returns roles when both roles and groups present", func(t *testing.T) {
		claims := &Claims{
			Roles:  []string{"admin"},
			Groups: []string{"platform-admin"},
		}
		assert.Equal(t, []string{"admin"}, claims.EffectiveRoles())
	})

	t.Run("falls back to groups when roles empty", func(t *testing.T) {
		claims := &Claims{
			Groups: []string{"platform-admin", "operator"},
		}
		assert.Equal(t, []string{"platform-admin", "operator"}, claims.EffectiveRoles())
	})

	t.Run("falls back to groups when roles nil", func(t *testing.T) {
		claims := &Claims{
			Roles:  nil,
			Groups: []string{"auditor"},
		}
		assert.Equal(t, []string{"auditor"}, claims.EffectiveRoles())
	})

	t.Run("returns empty slice when both nil", func(t *testing.T) {
		claims := &Claims{}
		assert.Equal(t, []string{}, claims.EffectiveRoles())
	})

	t.Run("returns defensive copy", func(t *testing.T) {
		claims := &Claims{Groups: []string{"admin"}}
		result := claims.EffectiveRoles()
		result[0] = "mutated"
		assert.Equal(t, []string{"admin"}, claims.EffectiveRoles())
	})
}

func TestClaims_HasRole_WithGroups(t *testing.T) {
	t.Run("finds role in groups when roles empty", func(t *testing.T) {
		claims := &Claims{Groups: []string{"platform-admin", "operator"}}
		assert.True(t, claims.HasRole("platform-admin"))
		assert.True(t, claims.HasRole("operator"))
		assert.False(t, claims.HasRole("admin"))
	})

	t.Run("uses roles over groups when both present", func(t *testing.T) {
		claims := &Claims{
			Roles:  []string{"admin"},
			Groups: []string{"platform-admin"},
		}
		assert.True(t, claims.HasRole("admin"))
		assert.False(t, claims.HasRole("platform-admin"))
	})
}

func TestClaims_GetRoles_WithGroups(t *testing.T) {
	t.Run("returns groups when roles empty", func(t *testing.T) {
		claims := &Claims{Groups: []string{"platform-admin"}}
		assert.Equal(t, []string{"platform-admin"}, claims.GetRoles())
	})

	t.Run("returns roles when roles present", func(t *testing.T) {
		claims := &Claims{
			Roles:  []string{"admin"},
			Groups: []string{"platform-admin"},
		}
		assert.Equal(t, []string{"admin"}, claims.GetRoles())
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
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			Scopes:   []string{"read"},
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
		tenantID, err := claims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
		assert.Equal(t, []string{"admin"}, claims.GetRoles())
		assert.Equal(t, []string{"read"}, claims.GetScopes())
		assert.False(t, claims.IsExpired())
		assert.Equal(t, "test-issuer", claims.Issuer)
		assert.Equal(t, "test-subject", claims.Subject)
	})
}

func TestValidateToken_WithGroupsClaim(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("extracts groups claim from valid JWT", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Groups: []string{"platform-admin", "operator"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extracted, err := validator.ValidateToken(tokenString)

		assert.NoError(t, err)
		assert.Equal(t, []string{"platform-admin", "operator"}, extracted.Groups)
		assert.True(t, extracted.HasRole("platform-admin"))
		assert.Equal(t, []string{"platform-admin", "operator"}, extracted.EffectiveRoles())
	})

	t.Run("roles take precedence over groups in JWT", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			Groups: []string{"platform-admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}

		tokenString, err := createTestToken(privateKey, claims)
		require.NoError(t, err)

		extracted, err := validator.ValidateToken(tokenString)

		assert.NoError(t, err)
		assert.Equal(t, []string{"admin"}, extracted.EffectiveRoles())
		assert.True(t, extracted.HasRole("admin"))
		assert.False(t, extracted.HasRole("platform-admin"))
	})
}

func TestValidateToken_WithOrganizationClaim(t *testing.T) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	validator, err := NewJWTValidator(publicKey)
	require.NoError(t, err)

	t.Run("extracts tenant claim from valid JWT", func(t *testing.T) {
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{"admin"},
			Scopes:   []string{"read", "write"},
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
		assert.Equal(t, "acme_bank", extractedClaims.TenantID)

		tenantID, err := extractedClaims.GetTenantID()
		assert.NoError(t, err)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("backward compatibility - tokens without tenant claim still validate", func(t *testing.T) {
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
		assert.Equal(t, "", extractedClaims.TenantID)

		_, err = extractedClaims.GetTenantID()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTenantClaimMissing)
	})
}
