package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockJWKSServer creates a test HTTP server that serves JWKS responses
func mockJWKSServer(t *testing.T, jwks JWKS) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(jwks)
		require.NoError(t, err)
	}))
}

// createTestJWKS creates a test JWKS with a single RSA key
func createTestJWKS(t *testing.T) (JWKS, *rsa.PrivateKey) {
	privateKey, publicKey, err := generateTestRSAKeys()
	require.NoError(t, err)

	// Convert public key to JWK format
	jwk := JWK{
		Kid: "test-key-1",
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		N:   base64URLEncode(publicKey.N.Bytes()),
		E:   base64URLEncode(intToBytes(publicKey.E)),
	}

	return JWKS{Keys: []JWK{jwk}}, privateKey
}

// base64URLEncode encodes bytes using base64 URL encoding without padding
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// intToBytes converts an integer to bytes
func intToBytes(n int) []byte {
	var bytes []byte
	for n > 0 {
		bytes = append([]byte{byte(n & 0xFF)}, bytes...)
		n >>= 8
	}
	if len(bytes) == 0 {
		return []byte{0}
	}
	return bytes
}

func TestNewJWKSProvider(t *testing.T) {
	ctx := context.Background()

	t.Run("success with valid configuration", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Hour,
		}

		provider, err := NewJWKSProvider(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, server.URL, provider.url)
		assert.Equal(t, 1*time.Hour, provider.cacheTTL)
	})

	t.Run("success with auto refresh", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:        server.URL,
			Client:     http.DefaultClient,
			CacheTTL:   1 * time.Minute,
			RefreshTTL: 45 * time.Second, // More than half of CacheTTL to avoid adjustment
		}

		provider, err := NewJWKSProvider(ctx, cfg)

		assert.NoError(t, err)
		assert.NotNil(t, provider)
		assert.Equal(t, 45*time.Second, provider.refreshTTL)

		// Clean up
		err = provider.Close()
		assert.NoError(t, err)
	})

	t.Run("error with empty URL", func(t *testing.T) {
		cfg := &JWKSProviderConfig{
			URL:    "",
			Client: http.DefaultClient,
		}

		provider, err := NewJWKSProvider(ctx, cfg)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrJWKSURLEmpty)
		assert.Nil(t, provider)
	})

	t.Run("error with nil HTTP client", func(t *testing.T) {
		cfg := &JWKSProviderConfig{
			URL:    "https://example.com/.well-known/jwks.json",
			Client: nil,
		}

		provider, err := NewJWKSProvider(ctx, cfg)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHTTPClientNil)
		assert.Nil(t, provider)
	})

	t.Run("error surfaces on GetKey when endpoint fails", func(t *testing.T) {
		// Create server that returns error
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		}

		// Provider creation succeeds (fetch is deferred)
		provider, err := NewJWKSProvider(ctx, cfg)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
		defer provider.Close()

		// Error surfaces on first GetKey call
		_, err = provider.GetKey(ctx, "test-kid")
		assert.Error(t, err)
	})

	t.Run("default cache TTL when not specified", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		}

		provider, err := NewJWKSProvider(ctx, cfg)

		assert.NoError(t, err)
		assert.Equal(t, 24*time.Hour, provider.cacheTTL)
	})
}

func TestJWKSProvider_GetKey(t *testing.T) {
	ctx := context.Background()

	t.Run("success getting cached key", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Hour,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		key, err := provider.GetKey(ctx, "test-key-1")

		assert.NoError(t, err)
		assert.NotNil(t, key)
	})

	t.Run("error when key not found", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Hour,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		key, err := provider.GetKey(ctx, "non-existent-key")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyNotFound)
		assert.Nil(t, key)
	})

	t.Run("refresh when cache expired", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Millisecond, // Very short TTL
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		//nolint:forbidigo // triggers cache TTL expiration to test cache refresh behavior
		time.Sleep(10 * time.Millisecond)

		key, err := provider.GetKey(ctx, "test-key-1")

		assert.NoError(t, err)
		assert.NotNil(t, key)
	})

	t.Run("return cached key when refresh fails", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		requestCount := 0

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestCount++
			if requestCount == 1 {
				// First request succeeds
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(jwks)
			} else {
				// Subsequent requests fail
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Millisecond,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		//nolint:forbidigo // triggers cache TTL expiration to test stale cache behavior with refresh failure
		time.Sleep(10 * time.Millisecond)

		// Should still return cached key even though refresh fails
		key, err := provider.GetKey(ctx, "test-key-1")

		assert.NoError(t, err)
		assert.NotNil(t, key)
	})
}

func TestParseRSAPublicKey(t *testing.T) {
	t.Run("success parsing valid JWK", func(t *testing.T) {
		_, publicKey, err := generateTestRSAKeys()
		require.NoError(t, err)

		jwk := JWK{
			Kid: "test-key",
			Kty: "RSA",
			N:   base64URLEncode(publicKey.N.Bytes()),
			E:   base64URLEncode(intToBytes(publicKey.E)),
		}

		parsedKey, err := parseRSAPublicKey(jwk)

		assert.NoError(t, err)
		assert.NotNil(t, parsedKey)
		assert.Equal(t, publicKey.N, parsedKey.N)
		assert.Equal(t, publicKey.E, parsedKey.E)
	})

	t.Run("error with non-RSA key type", func(t *testing.T) {
		jwk := JWK{
			Kid: "test-key",
			Kty: "EC", // Elliptic Curve, not RSA
		}

		parsedKey, err := parseRSAPublicKey(jwk)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidKeyType)
		assert.Nil(t, parsedKey)
	})

	t.Run("error with invalid modulus encoding", func(t *testing.T) {
		jwk := JWK{
			Kid: "test-key",
			Kty: "RSA",
			N:   "invalid-base64!!!",
			E:   "AQAB",
		}

		parsedKey, err := parseRSAPublicKey(jwk)

		assert.Error(t, err)
		assert.Nil(t, parsedKey)
	})
}

func TestJWTValidatorWithJWKS_ValidateToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success validating token with JWKS", func(t *testing.T) {
		jwks, privateKey := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:      server.URL,
			Client:   http.DefaultClient,
			CacheTTL: 1 * time.Hour,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		validator, err := NewJWTValidatorWithJWKS(provider)
		require.NoError(t, err)

		// Create token with kid in header
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "test-key-1"
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(ctx, tokenString)

		assert.NoError(t, err)
		assert.NotNil(t, extractedClaims)
		assert.Equal(t, "user-123", extractedClaims.UserID)
		assert.Equal(t, []string{"admin"}, extractedClaims.Roles)
	})

	t.Run("error with empty token string", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		validator, err := NewJWTValidatorWithJWKS(provider)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(ctx, "")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTokenStringEmpty)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error when token missing kid in header", func(t *testing.T) {
		jwks, privateKey := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		validator, err := NewJWTValidatorWithJWKS(provider)
		require.NoError(t, err)

		// Create token without kid
		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(ctx, tokenString)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTokenMissingKid)
		assert.Nil(t, extractedClaims)
	})

	t.Run("error when key not found for kid", func(t *testing.T) {
		jwks, privateKey := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:    server.URL,
			Client: http.DefaultClient,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		validator, err := NewJWTValidatorWithJWKS(provider)
		require.NoError(t, err)

		// Create token with non-existent kid
		claims := &Claims{
			UserID: "user-123",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "non-existent-key"
		tokenString, err := token.SignedString(privateKey)
		require.NoError(t, err)

		extractedClaims, err := validator.ValidateToken(ctx, tokenString)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrKeyNotFound)
		assert.Nil(t, extractedClaims)
	})
}

func TestNewJWTValidatorWithJWKS(t *testing.T) {
	t.Run("error with nil provider", func(t *testing.T) {
		validator, err := NewJWTValidatorWithJWKS(nil)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrJWKSProviderNil)
		assert.Nil(t, validator)
	})
}

func TestJWKSProvider_Close(t *testing.T) {
	ctx := context.Background()

	t.Run("close stops auto refresh", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:        server.URL,
			Client:     http.DefaultClient,
			RefreshTTL: 100 * time.Millisecond,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		err = provider.Close()
		assert.NoError(t, err)

		//nolint:forbidigo // gives auto-refresh goroutine time to exit after provider.Close()
		time.Sleep(200 * time.Millisecond)
	})

	t.Run("close is idempotent", func(t *testing.T) {
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		cfg := &JWKSProviderConfig{
			URL:        server.URL,
			Client:     http.DefaultClient,
			RefreshTTL: 100 * time.Millisecond,
		}

		provider, err := NewJWKSProvider(ctx, cfg)
		require.NoError(t, err)

		// Close multiple times should not panic
		err = provider.Close()
		assert.NoError(t, err)

		err = provider.Close()
		assert.NoError(t, err)

		err = provider.Close()
		assert.NoError(t, err)
	})
}
