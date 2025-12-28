package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthInterceptor_Disabled(t *testing.T) {
	t.Run("returns nil when AUTH_ENABLED=false", func(t *testing.T) {
		t.Setenv("AUTH_ENABLED", "false")

		ctx := context.Background()
		cfg := DefaultAuthConfig(nil)

		interceptor, err := NewAuthInterceptor(ctx, cfg)

		require.NoError(t, err)
		assert.Nil(t, interceptor)
	})

	t.Run("logs warning when disabled with logger", func(t *testing.T) {
		t.Setenv("AUTH_ENABLED", "false")

		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		cfg := DefaultAuthConfig(logger)

		interceptor, err := NewAuthInterceptor(ctx, cfg)

		require.NoError(t, err)
		assert.Nil(t, interceptor)
	})

	t.Run("Enabled=false in config returns nil", func(t *testing.T) {
		ctx := context.Background()
		cfg := AuthConfig{
			Enabled: false,
			JWKSURL: "http://example.com/jwks", // Even with URL, should return nil
		}

		interceptor, err := NewAuthInterceptor(ctx, cfg)

		require.NoError(t, err)
		assert.Nil(t, interceptor)
	})
}

func TestNewAuthInterceptor_MissingJWKSURL(t *testing.T) {
	t.Run("returns error when enabled but JWKS URL missing", func(t *testing.T) {
		t.Setenv("AUTH_ENABLED", "true")
		t.Setenv("AUTH_JWKS_URL", "")

		ctx := context.Background()
		cfg := DefaultAuthConfig(nil)

		_, err := NewAuthInterceptor(ctx, cfg)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAuthMissingJWKSURL)
	})

	t.Run("returns error when Enabled=true but JWKSURL empty", func(t *testing.T) {
		ctx := context.Background()
		cfg := AuthConfig{
			Enabled: true,
			JWKSURL: "",
		}

		_, err := NewAuthInterceptor(ctx, cfg)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAuthMissingJWKSURL)
		assert.Contains(t, err.Error(), "AUTH_JWKS_URL is required")
	})
}

func TestDefaultBypassMethods(t *testing.T) {
	t.Run("returns expected health check methods", func(t *testing.T) {
		methods := DefaultBypassMethods()

		assert.Contains(t, methods, "/grpc.health.v1.Health/Check")
		assert.Contains(t, methods, "/grpc.health.v1.Health/Watch")
	})

	t.Run("returns expected reflection methods", func(t *testing.T) {
		methods := DefaultBypassMethods()

		assert.Contains(t, methods, "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo")
		assert.Contains(t, methods, "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo")
	})

	t.Run("returns four methods total", func(t *testing.T) {
		methods := DefaultBypassMethods()

		assert.Len(t, methods, 4)
	})
}

func TestDefaultAuthConfig(t *testing.T) {
	t.Run("uses defaults when environment variables not set", func(t *testing.T) {
		os.Unsetenv("AUTH_ENABLED")
		os.Unsetenv("AUTH_JWKS_URL")
		os.Unsetenv("AUTH_JWKS_REFRESH_TTL")
		os.Unsetenv("AUTH_HTTP_TIMEOUT")

		cfg := DefaultAuthConfig(nil)

		assert.True(t, cfg.Enabled) // Default: true
		assert.Equal(t, "", cfg.JWKSURL)
		assert.Equal(t, 30*time.Minute, cfg.RefreshTTL)
		assert.Equal(t, 30*time.Second, cfg.HTTPTimeout)
		assert.Equal(t, DefaultBypassMethods(), cfg.BypassMethods)
		assert.Nil(t, cfg.Logger)
	})

	t.Run("reads from environment variables", func(t *testing.T) {
		t.Setenv("AUTH_ENABLED", "false")
		t.Setenv("AUTH_JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
		t.Setenv("AUTH_JWKS_REFRESH_TTL", "1h")
		t.Setenv("AUTH_HTTP_TIMEOUT", "15s")

		cfg := DefaultAuthConfig(nil)

		assert.False(t, cfg.Enabled)
		assert.Equal(t, "https://auth.example.com/.well-known/jwks.json", cfg.JWKSURL)
		assert.Equal(t, 1*time.Hour, cfg.RefreshTTL)
		assert.Equal(t, 15*time.Second, cfg.HTTPTimeout)
	})

	t.Run("handles invalid duration values gracefully", func(t *testing.T) {
		t.Setenv("AUTH_JWKS_REFRESH_TTL", "invalid")
		t.Setenv("AUTH_HTTP_TIMEOUT", "also-invalid")

		cfg := DefaultAuthConfig(nil)

		// Should fall back to defaults
		assert.Equal(t, 30*time.Minute, cfg.RefreshTTL)
		assert.Equal(t, 30*time.Second, cfg.HTTPTimeout)
	})

	t.Run("handles invalid bool values gracefully", func(t *testing.T) {
		t.Setenv("AUTH_ENABLED", "not-a-bool")

		cfg := DefaultAuthConfig(nil)

		// Should fall back to default (true)
		assert.True(t, cfg.Enabled)
	})

	t.Run("preserves logger when provided", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		cfg := DefaultAuthConfig(logger)

		assert.NotNil(t, cfg.Logger)
		assert.Equal(t, logger, cfg.Logger)
	})
}

func TestAuthConfig(t *testing.T) {
	t.Run("fields are accessible", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		bypassMethods := []string{"/custom/Method"}

		cfg := AuthConfig{
			Enabled:       true,
			JWKSURL:       "https://example.com/jwks",
			RefreshTTL:    45 * time.Minute,
			HTTPTimeout:   20 * time.Second,
			BypassMethods: bypassMethods,
			Logger:        logger,
		}

		assert.True(t, cfg.Enabled)
		assert.Equal(t, "https://example.com/jwks", cfg.JWKSURL)
		assert.Equal(t, 45*time.Minute, cfg.RefreshTTL)
		assert.Equal(t, 20*time.Second, cfg.HTTPTimeout)
		assert.Equal(t, bypassMethods, cfg.BypassMethods)
		assert.NotNil(t, cfg.Logger)
	})
}
