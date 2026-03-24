package gateway

import (
	"io"
	"log/slog"
	"testing"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAuthMiddleware_WithJWKSURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := AuthConfig{
		JWKSURL: "https://example.com/.well-known/jwks.json",
	}

	// NewJWKSProvider defers initial fetch, so any non-empty URL succeeds at creation.
	middleware, err := BuildAuthMiddleware(config, logger)

	require.NoError(t, err)
	assert.NotNil(t, middleware)
	middleware.Close()
}

func TestBuildAuthMiddleware_EmptyJWKSURL(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := AuthConfig{
		JWKSURL: "", // empty URL should fail
	}

	_, err := BuildAuthMiddleware(config, logger)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create JWKS provider")
}

func TestBuildAuthMiddleware_WithLocalSigner(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{})
	require.NoError(t, err)

	config := AuthConfig{
		JWKSURL: "https://example.com/.well-known/jwks.json",
	}

	middleware, err := BuildAuthMiddleware(config, logger, signer)

	require.NoError(t, err)
	assert.NotNil(t, middleware)
	middleware.Close()
}

func TestBuildAuthMiddleware_WithAPIKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := AuthConfig{
		JWKSURL: "https://example.com/.well-known/jwks.json",
		APIKeys: map[string]string{
			"api-key-1": "service-a",
			"api-key-2": "service-b",
		},
		RateLimitPerSecond: 100,
		RateLimitBurst:     200,
	}

	middleware, err := BuildAuthMiddleware(config, logger)

	require.NoError(t, err)
	assert.NotNil(t, middleware)
	middleware.Close()
}

func TestBuildAuthMiddleware_CloseIsIdempotent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := AuthConfig{
		JWKSURL: "https://example.com/.well-known/jwks.json",
		APIKeys: map[string]string{"key": "identity"},
	}

	middleware, err := BuildAuthMiddleware(config, logger)
	require.NoError(t, err)

	// Close should be safe to call multiple times.
	assert.NotPanics(t, func() {
		middleware.Close()
		middleware.Close()
	})
}

func TestBuildAuthMiddleware_WithLocalSigner_NilSignerIgnored(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	config := AuthConfig{
		JWKSURL: "https://example.com/.well-known/jwks.json",
	}

	// Passing a nil signer should fall back to dex-only path.
	middleware, err := BuildAuthMiddleware(config, logger, nil)

	require.NoError(t, err)
	assert.NotNil(t, middleware)
	middleware.Close()
}
