package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJWTSigner_AutoGenerateKey(t *testing.T) {
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{})
	require.NoError(t, err)
	assert.NotNil(t, signer.PublicKey())
	assert.Equal(t, "meridian-1", signer.KeyID())
	assert.Equal(t, "meridian", signer.Issuer())
}

func TestNewJWTSigner_WithPEMKey(t *testing.T) {
	pemKey := generateTestPEM(t)

	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyPEM: pemKey,
		KeyID:         "test-key-1",
		Issuer:        "test-issuer",
	})
	require.NoError(t, err)
	assert.Equal(t, "test-key-1", signer.KeyID())
	assert.Equal(t, "test-issuer", signer.Issuer())
}

func TestNewJWTSigner_InvalidPEM(t *testing.T) {
	_, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyPEM: "not-a-pem",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidPEM)
}

func TestJWTSigner_SignAndVerify(t *testing.T) {
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		KeyID:  "test-1",
		Issuer: "test-meridian",
	})
	require.NoError(t, err)

	claims := map[string]interface{}{
		"sub":         "user-123",
		"email":       "test@example.com",
		"x-tenant-id": "volterra",
		"roles":       []string{"operator"},
	}

	tokenStr, err := signer.SignClaims(claims, 1*time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)

	// Verify with the public key
	validator, err := auth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)

	parsed, err := validator.ValidateToken(tokenStr)
	require.NoError(t, err)
	assert.Equal(t, "user-123", parsed.Subject)
	assert.Equal(t, "test@example.com", parsed.Email)
	assert.Equal(t, "volterra", parsed.TenantID)
	assert.Equal(t, []string{"operator"}, parsed.Roles)
}

func TestJWTSigner_TokenHasKid(t *testing.T) {
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		KeyID: "my-kid",
	})
	require.NoError(t, err)

	tokenStr, err := signer.SignClaims(map[string]interface{}{
		"sub": "u1",
	}, time.Hour)
	require.NoError(t, err)

	// Parse without verification to check header
	parser := &jwt.Parser{}
	token, _, err := parser.ParseUnverified(tokenStr, jwt.MapClaims{})
	require.NoError(t, err)
	assert.Equal(t, "my-kid", token.Header["kid"])
}

func TestJWTSigner_JWKS(t *testing.T) {
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		KeyID: "jwks-test",
	})
	require.NoError(t, err)

	jwks := signer.JWKS()
	require.Len(t, jwks.Keys, 1)
	assert.Equal(t, "jwks-test", jwks.Keys[0].Kid)
	assert.Equal(t, "RSA", jwks.Keys[0].Kty)
	assert.Equal(t, "sig", jwks.Keys[0].Use)
	assert.Equal(t, "RS256", jwks.Keys[0].Alg)
	assert.NotEmpty(t, jwks.Keys[0].N)
	assert.NotEmpty(t, jwks.Keys[0].E)
}

func TestJWTSigner_ServeJWKS(t *testing.T) {
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		KeyID: "serve-test",
	})
	require.NoError(t, err)

	handler := signer.ServeJWKS()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/jwks", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "public")

	var jwks auth.JWKS
	err = json.NewDecoder(rec.Body).Decode(&jwks)
	require.NoError(t, err)
	require.Len(t, jwks.Keys, 1)
	assert.Equal(t, "serve-test", jwks.Keys[0].Kid)
}

func TestJWTSigner_RoundTripWithJWKS(t *testing.T) {
	// Simulate: signer produces token → JWKS served → validator uses JWKS to verify
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		KeyID:  "roundtrip",
		Issuer: "meridian-test",
	})
	require.NoError(t, err)

	// Sign a token
	tokenStr, err := signer.SignClaims(map[string]interface{}{
		"sub":         "user-456",
		"email":       "admin@acme.com",
		"x-tenant-id": "acme",
		"roles":       []string{"platform-admin", "super-admin"},
	}, time.Hour)
	require.NoError(t, err)

	// Serve JWKS
	srv := httptest.NewServer(signer.ServeJWKS())
	defer srv.Close()

	// Create validator from JWKS endpoint
	provider, err := auth.NewJWKSProvider(t.Context(), &auth.JWKSProviderConfig{
		URL:      srv.URL,
		Client:   srv.Client(),
		CacheTTL: time.Minute,
	})
	require.NoError(t, err)
	defer func() { _ = provider.Close() }()

	validator, err := auth.NewJWTValidatorWithJWKS(provider)
	require.NoError(t, err)

	parsed, err := validator.ValidateToken(t.Context(), tokenStr)
	require.NoError(t, err)
	assert.Equal(t, "user-456", parsed.Subject)
	assert.Equal(t, "admin@acme.com", parsed.Email)
	assert.Equal(t, "acme", parsed.TenantID)
	assert.Equal(t, []string{"platform-admin", "super-admin"}, parsed.Roles)
}

func TestNewJWTSigner_WithKeyFile(t *testing.T) {
	pemKey := generateTestPEM(t)

	// Write PEM to a temp file.
	f, err := os.CreateTemp(t.TempDir(), "jwt-key-*.pem")
	require.NoError(t, err)
	_, err = f.WriteString(pemKey)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyFile: f.Name(),
		KeyID:          "file-key-1",
		Issuer:         "test-issuer",
	})
	require.NoError(t, err)
	assert.Equal(t, "file-key-1", signer.KeyID())
	assert.Equal(t, "test-issuer", signer.Issuer())

	// Verify the signer works end-to-end.
	tokenStr, err := signer.SignClaims(map[string]interface{}{
		"sub": "user-from-file",
	}, time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)
}

func TestNewJWTSigner_KeyFileTakesPrecedenceOverPEM(t *testing.T) {
	pemKey := generateTestPEM(t)

	f, err := os.CreateTemp(t.TempDir(), "jwt-key-*.pem")
	require.NoError(t, err)
	_, err = f.WriteString(pemKey)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// PrivateKeyFile wins; PrivateKeyPEM is intentionally invalid.
	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyFile: f.Name(),
		PrivateKeyPEM:  "not-a-pem",
	})
	require.NoError(t, err)
	assert.NotNil(t, signer.PublicKey())
}

func TestNewJWTSigner_KeyFileNotFound(t *testing.T) {
	_, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyFile: "/nonexistent/path/key.pem",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read key file")
}

func TestNewJWTSigner_EscapedNewlinePEM(t *testing.T) {
	// Simulate environment variable injection where real newlines
	// are replaced with literal \n (e.g., docker-compose .env files).
	pemKey := generateTestPEM(t)
	escapedPEM := strings.ReplaceAll(pemKey, "\n", `\n`)

	signer, err := auth.NewJWTSigner(auth.JWTSignerConfig{
		PrivateKeyPEM: escapedPEM,
		KeyID:         "escaped-key",
	})
	require.NoError(t, err)

	// Verify the signer works end-to-end.
	tokenStr, err := signer.SignClaims(map[string]interface{}{
		"sub": "user-escaped",
	}, time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)
}

func generateTestPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}
