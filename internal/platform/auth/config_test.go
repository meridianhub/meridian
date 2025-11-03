package auth

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// setEnv is a test helper that sets an environment variable and fails the test on error
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("Failed to set env var %s: %v", key, err)
	}
}

func TestNewConfigFromEnv(t *testing.T) {
	// Save original env and restore after tests
	originalEnv := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range originalEnv {
			for i := 0; i < len(env); i++ {
				if env[i] == '=' {
					if err := os.Setenv(env[:i], env[i+1:]); err != nil {
						t.Logf("Failed to restore env var: %v", err)
					}
					break
				}
			}
		}
	}()

	t.Run("JWKS mode with minimal config", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "jwks")
		setEnv(t, "JWKS_URL", "https://auth.example.com/.well-known/jwks.json")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, AuthModeJWKS, config.Mode)
		assert.Equal(t, "https://auth.example.com/.well-known/jwks.json", config.JWKSURL)
		assert.Equal(t, 24*time.Hour, config.JWKSCacheTTL)
		assert.Equal(t, time.Duration(0), config.JWKSRefreshTTL)
	})

	t.Run("JWKS mode with full config", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "jwks")
		setEnv(t, "JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
		setEnv(t, "JWKS_CACHE_TTL", "1h")
		setEnv(t, "JWKS_REFRESH_TTL", "30m")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, AuthModeJWKS, config.Mode)
		assert.Equal(t, time.Hour, config.JWKSCacheTTL)
		assert.Equal(t, 30*time.Minute, config.JWKSRefreshTTL)
	})

	t.Run("JWKS mode defaults when AUTH_MODE not set", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "JWKS_URL", "https://auth.example.com/.well-known/jwks.json")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, AuthModeJWKS, config.Mode)
	})

	t.Run("JWKS mode error when JWKS_URL missing", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "jwks")

		config, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingJWKSURL)
		assert.Equal(t, Config{}, config)
	})

	t.Run("JWKS mode error with invalid cache TTL", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "jwks")
		setEnv(t, "JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
		setEnv(t, "JWKS_CACHE_TTL", "invalid")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JWKS_CACHE_TTL")
	})

	t.Run("JWKS mode error with invalid refresh TTL", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "jwks")
		setEnv(t, "JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
		setEnv(t, "JWKS_REFRESH_TTL", "invalid")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JWKS_REFRESH_TTL")
	})

	t.Run("OAuth mode with minimal config", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_ID", "test-client")
		setEnv(t, "OAUTH_CLIENT_SECRET", "test-secret")
		setEnv(t, "OAUTH_TOKEN_URL", "https://auth.example.com/oauth/token")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, AuthModeOAuth, config.Mode)
		assert.Equal(t, "test-client", config.OAuthClientID)
		assert.Equal(t, "test-secret", config.OAuthClientSecret)
		assert.Equal(t, "https://auth.example.com/oauth/token", config.OAuthTokenURL)
		assert.Nil(t, config.OAuthScopes)
	})

	t.Run("OAuth mode with scopes", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_ID", "test-client")
		setEnv(t, "OAUTH_CLIENT_SECRET", "test-secret")
		setEnv(t, "OAUTH_TOKEN_URL", "https://auth.example.com/oauth/token")
		setEnv(t, "OAUTH_SCOPES", "read:data, write:data, admin:users")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, []string{"read:data", "write:data", "admin:users"}, config.OAuthScopes)
	})

	t.Run("OAuth mode with introspection URL", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_ID", "test-client")
		setEnv(t, "OAUTH_CLIENT_SECRET", "test-secret")
		setEnv(t, "OAUTH_TOKEN_URL", "https://auth.example.com/oauth/token")
		setEnv(t, "OAUTH_INTROSPECTION_URL", "https://auth.example.com/oauth/introspect")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, "https://auth.example.com/oauth/introspect", config.OAuthIntrospectionURL)
	})

	t.Run("OAuth mode error when client ID missing", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_SECRET", "test-secret")
		setEnv(t, "OAUTH_TOKEN_URL", "https://auth.example.com/oauth/token")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "OAUTH_CLIENT_ID")
	})

	t.Run("OAuth mode error when client secret missing", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_ID", "test-client")
		setEnv(t, "OAUTH_TOKEN_URL", "https://auth.example.com/oauth/token")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "OAUTH_CLIENT_SECRET")
	})

	t.Run("OAuth mode error when token URL missing", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "oauth")
		setEnv(t, "OAUTH_CLIENT_ID", "test-client")
		setEnv(t, "OAUTH_CLIENT_SECRET", "test-secret")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "OAUTH_TOKEN_URL")
	})

	t.Run("disabled mode", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "disabled")

		config, err := NewConfigFromEnv()

		assert.NoError(t, err)
		assert.Equal(t, AuthModeDisabled, config.Mode)
	})

	t.Run("invalid auth mode", func(t *testing.T) {
		os.Clearenv()
		setEnv(t, "AUTH_MODE", "invalid")

		_, err := NewConfigFromEnv()

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidAuthMode)
	})
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, AuthModeJWKS, config.Mode)
	assert.Equal(t, "http://localhost:8080/realms/meridian/protocol/openid-connect/certs", config.JWKSURL)
	assert.Equal(t, time.Hour, config.JWKSCacheTTL)
	assert.Equal(t, 30*time.Minute, config.JWKSRefreshTTL)
	assert.NotNil(t, config.HTTPClient)
}

func TestConfig_NewAuthenticator(t *testing.T) {
	ctx := context.Background()

	t.Run("JWKS mode creates interceptor", func(t *testing.T) {
		// Create test JWKS server
		jwks, _ := createTestJWKS(t)
		server := mockJWKSServer(t, jwks)
		defer server.Close()

		config := Config{
			Mode:           AuthModeJWKS,
			JWKSURL:        server.URL,
			JWKSCacheTTL:   time.Hour,
			JWKSRefreshTTL: 0,
			HTTPClient:     http.DefaultClient,
		}

		interceptor, err := config.NewAuthenticator(ctx)

		assert.NoError(t, err)
		assert.NotNil(t, interceptor)
	})

	t.Run("OAuth mode returns error", func(t *testing.T) {
		config := Config{
			Mode:              AuthModeOAuth,
			OAuthClientID:     "test-client",
			OAuthClientSecret: "test-secret",
			OAuthTokenURL:     "https://auth.example.com/oauth/token",
		}

		interceptor, err := config.NewAuthenticator(ctx)

		assert.Error(t, err)
		assert.Nil(t, interceptor)
		assert.Contains(t, err.Error(), "OAuth mode does not support inbound authentication")
	})

	t.Run("disabled mode returns nil", func(t *testing.T) {
		config := Config{
			Mode: AuthModeDisabled,
		}

		interceptor, err := config.NewAuthenticator(ctx)

		assert.NoError(t, err)
		assert.Nil(t, interceptor)
	})

	t.Run("error when JWKS provider creation fails", func(t *testing.T) {
		config := Config{
			Mode:         AuthModeJWKS,
			JWKSURL:      "", // Invalid - empty URL
			HTTPClient:   http.DefaultClient,
			JWKSCacheTTL: time.Hour,
		}

		interceptor, err := config.NewAuthenticator(ctx)

		assert.Error(t, err)
		assert.Nil(t, interceptor)
	})
}

func TestConfig_NewOAuthClient(t *testing.T) {
	t.Run("creates OAuth client in OAuth mode", func(t *testing.T) {
		config := Config{
			Mode:              AuthModeOAuth,
			OAuthClientID:     "test-client",
			OAuthClientSecret: "test-secret",
			OAuthTokenURL:     "https://auth.example.com/oauth/token",
			OAuthScopes:       []string{"read", "write"},
			HTTPClient:        http.DefaultClient,
		}

		client, err := config.NewOAuthClient()

		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("error when not in OAuth mode", func(t *testing.T) {
		config := Config{
			Mode: AuthModeJWKS,
		}

		client, err := config.NewOAuthClient()

		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "requires auth mode 'oauth'")
	})
}

func TestConfig_NewIntrospector(t *testing.T) {
	t.Run("creates introspector in OAuth mode", func(t *testing.T) {
		config := Config{
			Mode:                  AuthModeOAuth,
			OAuthClientID:         "test-client",
			OAuthClientSecret:     "test-secret",
			OAuthIntrospectionURL: "https://auth.example.com/oauth/introspect",
			HTTPClient:            http.DefaultClient,
		}

		introspector, err := config.NewIntrospector()

		assert.NoError(t, err)
		assert.NotNil(t, introspector)
	})

	t.Run("error when not in OAuth mode", func(t *testing.T) {
		config := Config{
			Mode: AuthModeJWKS,
		}

		introspector, err := config.NewIntrospector()

		assert.Error(t, err)
		assert.Nil(t, introspector)
		assert.Contains(t, err.Error(), "requires auth mode 'oauth'")
	})

	t.Run("error when introspection URL not set", func(t *testing.T) {
		config := Config{
			Mode:              AuthModeOAuth,
			OAuthClientID:     "test-client",
			OAuthClientSecret: "test-secret",
		}

		introspector, err := config.NewIntrospector()

		assert.Error(t, err)
		assert.Nil(t, introspector)
		assert.Contains(t, err.Error(), "OAUTH_INTROSPECTION_URL is required")
	})
}

func TestSplitScopes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single scope",
			input:    "read",
			expected: []string{"read"},
		},
		{
			name:     "multiple scopes",
			input:    "read,write,admin",
			expected: []string{"read", "write", "admin"},
		},
		{
			name:     "scopes with spaces",
			input:    "read, write, admin",
			expected: []string{"read", "write", "admin"},
		},
		{
			name:     "scopes with colons",
			input:    "read:data, write:data, admin:users",
			expected: []string{"read:data", "write:data", "admin:users"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "trailing comma",
			input:    "read,write,",
			expected: []string{"read", "write"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitScopes(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
