package auth

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceAuthConfigFromEnv(t *testing.T) {
	// Save and restore environment
	savedEnv := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, e := range savedEnv {
			k, v, _ := splitEnvVar(e)
			os.Setenv(k, v)
		}
	})

	t.Run("disabled by default when env var not set", func(t *testing.T) {
		os.Unsetenv("SERVICE_AUTH_ENABLED")
		os.Unsetenv("SERVICE_CLIENT_ID")
		os.Unsetenv("SERVICE_CLIENT_SECRET")
		os.Unsetenv("SERVICE_TOKEN_URL")
		os.Unsetenv("SERVICE_SCOPES")

		cfg := NewServiceAuthConfigFromEnv()
		assert.False(t, cfg.Enabled)
	})

	t.Run("disabled when explicitly set to false", func(t *testing.T) {
		os.Setenv("SERVICE_AUTH_ENABLED", "false")

		cfg := NewServiceAuthConfigFromEnv()
		assert.False(t, cfg.Enabled)
	})

	t.Run("enabled with all required fields", func(t *testing.T) {
		os.Setenv("SERVICE_AUTH_ENABLED", "true")
		os.Setenv("SERVICE_CLIENT_ID", "meridian-service")
		os.Setenv("SERVICE_CLIENT_SECRET", "super-secret")
		os.Setenv("SERVICE_TOKEN_URL", "https://auth.example.com/token")
		os.Setenv("SERVICE_SCOPES", "service.read,service.write")

		cfg := NewServiceAuthConfigFromEnv()
		assert.True(t, cfg.Enabled)
		assert.Equal(t, "meridian-service", cfg.ClientID)
		assert.Equal(t, "super-secret", cfg.ClientSecret)
		assert.Equal(t, "https://auth.example.com/token", cfg.TokenURL)
		assert.Equal(t, []string{"service.read", "service.write"}, cfg.Scopes)
	})

	t.Run("no scopes when env var empty", func(t *testing.T) {
		os.Setenv("SERVICE_AUTH_ENABLED", "true")
		os.Setenv("SERVICE_CLIENT_ID", "svc")
		os.Setenv("SERVICE_CLIENT_SECRET", "secret")
		os.Setenv("SERVICE_TOKEN_URL", "https://auth.example.com/token")
		os.Unsetenv("SERVICE_SCOPES")

		cfg := NewServiceAuthConfigFromEnv()
		assert.True(t, cfg.Enabled)
		assert.Nil(t, cfg.Scopes)
	})
}

func TestServiceAuthConfig_NewCredentials(t *testing.T) {
	t.Run("returns nil when disabled", func(t *testing.T) {
		cfg := ServiceAuthConfig{Enabled: false}
		creds, err := cfg.NewCredentials()
		assert.NoError(t, err)
		assert.Nil(t, creds)
	})

	t.Run("returns error when enabled but missing client ID", func(t *testing.T) {
		cfg := ServiceAuthConfig{
			Enabled:      true,
			ClientSecret: "secret",
			TokenURL:     "https://auth.example.com/token",
		}
		creds, err := cfg.NewCredentials()
		assert.Error(t, err)
		assert.Nil(t, creds)
	})

	t.Run("returns error when enabled but missing client secret", func(t *testing.T) {
		cfg := ServiceAuthConfig{
			Enabled:  true,
			ClientID: "client",
			TokenURL: "https://auth.example.com/token",
		}
		creds, err := cfg.NewCredentials()
		assert.Error(t, err)
		assert.Nil(t, creds)
	})

	t.Run("returns error when enabled but missing token URL", func(t *testing.T) {
		cfg := ServiceAuthConfig{
			Enabled:      true,
			ClientID:     "client",
			ClientSecret: "secret",
		}
		creds, err := cfg.NewCredentials()
		assert.Error(t, err)
		assert.Nil(t, creds)
	})

	t.Run("returns credentials when fully configured", func(t *testing.T) {
		cfg := ServiceAuthConfig{
			Enabled:      true,
			ClientID:     "meridian-service",
			ClientSecret: "super-secret",
			TokenURL:     "https://auth.example.com/token",
			Scopes:       []string{"service.read"},
		}
		creds, err := cfg.NewCredentials()
		assert.NoError(t, err)
		assert.NotNil(t, creds)
	})
}

// splitEnvVar splits "KEY=VALUE" into key, value, ok
func splitEnvVar(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

func TestNewServiceCredentialsDialOption(t *testing.T) {
	t.Run("returns nil when credentials nil", func(t *testing.T) {
		opt := NewServiceCredentialsDialOption(nil)
		assert.Nil(t, opt)
	})

	t.Run("returns dial option when credentials provided", func(t *testing.T) {
		cfg := ServiceAuthConfig{
			Enabled:      true,
			ClientID:     "svc",
			ClientSecret: "secret",
			TokenURL:     "https://auth.example.com/token",
		}
		creds, err := cfg.NewCredentials()
		require.NoError(t, err)

		opt := NewServiceCredentialsDialOption(creds)
		assert.NotNil(t, opt)
	})
}
