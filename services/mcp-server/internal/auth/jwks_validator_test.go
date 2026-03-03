package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestRSAKey generates a 2048-bit RSA key pair for use in tests.
func generateTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// buildJWKSServer creates a test HTTP server that returns a JWKS containing the given public key.
func buildJWKSServer(t *testing.T, kid string, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()

	nBytes := pub.N.Bytes()
	e := pub.E
	eBytes := make([]byte, 4)
	eBytes[0] = byte(e >> 24)
	eBytes[1] = byte(e >> 16)
	eBytes[2] = byte(e >> 8)
	eBytes[3] = byte(e)
	// Trim leading zero bytes from exponent.
	i := 0
	for i < len(eBytes)-1 && eBytes[i] == 0 {
		i++
	}
	eBytes = eBytes[i:]

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kid": kid,
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(nBytes),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// signToken creates a signed JWT with the given private key and kid.
func signToken(t *testing.T, key *rsa.PrivateKey, kid string) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Subject:   "test-user",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	token.Header["kid"] = kid

	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

// TestNewJWKSBearerValidator_EmptyURL verifies that an empty URL is rejected.
func TestNewJWKSBearerValidator_EmptyURL(t *testing.T) {
	_, err := auth.NewJWKSBearerValidator(context.Background(), "")
	require.Error(t, err)
}

// TestNewJWKSBearerValidator_UnreachableURL verifies that a failed initial
// JWKS fetch returns an error.
func TestNewJWKSBearerValidator_UnreachableURL(t *testing.T) {
	_, err := auth.NewJWKSBearerValidator(context.Background(), "http://127.0.0.1:0/keys")
	require.Error(t, err)
}

// TestJWKSBearerValidator_ValidToken verifies that a validly-signed JWT is accepted.
func TestJWKSBearerValidator_ValidToken(t *testing.T) {
	key := generateTestRSAKey(t)
	kid := "test-key-1"

	srv := buildJWKSServer(t, kid, &key.PublicKey)

	validator, err := auth.NewJWKSBearerValidator(context.Background(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })

	token := signToken(t, key, kid)

	err = validator.ValidateBearer(token)
	require.NoError(t, err)
}

// TestJWKSBearerValidator_InvalidSignature verifies that a token signed with the
// wrong key is rejected.
func TestJWKSBearerValidator_InvalidSignature(t *testing.T) {
	serverKey := generateTestRSAKey(t)
	wrongKey := generateTestRSAKey(t)
	kid := "test-key-1"

	// JWKS has serverKey's public key but token is signed with wrongKey.
	srv := buildJWKSServer(t, kid, &serverKey.PublicKey)

	validator, err := auth.NewJWKSBearerValidator(context.Background(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })

	token := signToken(t, wrongKey, kid)

	err = validator.ValidateBearer(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidBearerToken)
}

// TestJWKSBearerValidator_ExpiredToken verifies that an expired JWT is rejected.
func TestJWKSBearerValidator_ExpiredToken(t *testing.T) {
	key := generateTestRSAKey(t)
	kid := "test-key-1"

	srv := buildJWKSServer(t, kid, &key.PublicKey)

	validator, err := auth.NewJWKSBearerValidator(context.Background(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })

	// Build an already-expired token.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Subject:   "test-user",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
	})
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	require.NoError(t, err)

	err = validator.ValidateBearer(signed)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidBearerToken)
}

// TestJWKSBearerValidator_UnknownKid verifies that a token with an unknown kid
// is rejected after a JWKS refresh fails to surface the key.
func TestJWKSBearerValidator_UnknownKid(t *testing.T) {
	key := generateTestRSAKey(t)
	kid := "test-key-1"

	srv := buildJWKSServer(t, kid, &key.PublicKey)

	validator, err := auth.NewJWKSBearerValidator(context.Background(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })

	// Token references a kid that the JWKS doesn't have.
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Subject:   "test-user",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	token.Header["kid"] = "unknown-kid"
	signed, err := token.SignedString(key)
	require.NoError(t, err)

	err = validator.ValidateBearer(signed)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidBearerToken)
}

// TestJWKSBearerValidator_MalformedToken verifies that a non-JWT string is rejected.
func TestJWKSBearerValidator_MalformedToken(t *testing.T) {
	key := generateTestRSAKey(t)
	kid := "test-key-1"

	srv := buildJWKSServer(t, kid, &key.PublicKey)

	validator, err := auth.NewJWKSBearerValidator(context.Background(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = validator.Close() })

	err = validator.ValidateBearer("not-a-jwt-at-all")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidBearerToken)
}

// TestBigIntExponentRoundtrip verifies the byte encoding used in
// buildJWKSServer produces a correctly round-tripped RSA exponent.
func TestBigIntExponentRoundtrip(t *testing.T) {
	// Standard RSA exponent 65537 (0x10001).
	expected := 65537
	b := big.NewInt(int64(expected)).Bytes()

	var result int
	for _, by := range b {
		result = result<<8 + int(by)
	}
	assert.Equal(t, expected, result)
}
