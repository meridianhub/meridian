package gateway

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Success(t *testing.T) {
	// Save and restore environment
	cleanup := setEnvVars(t, map[string]string{
		"PORT":           "9090",
		"BASE_DOMAIN":    "api.example.com",
		"LOCAL_DEV_MODE": "true",
		"DATABASE_URL":   "postgres://user@localhost/db",
		"REDIS_URL":      "redis://localhost:6379",
		"BACKEND_ROUTES": `[{"prefix":"/v1/party","target":"party:50055"}]`,
	})
	defer cleanup()

	config, err := LoadConfig()

	require.NoError(t, err)
	assert.Equal(t, 9090, config.Port)
	assert.Equal(t, "api.example.com", config.BaseDomain)
	assert.True(t, config.LocalDevMode)
	assert.Equal(t, "postgres://user@localhost/db", config.DatabaseURL)
	assert.Equal(t, "redis://localhost:6379", config.RedisURL)
	require.Len(t, config.Backends, 1)
	assert.Equal(t, "/v1/party", config.Backends[0].Prefix)
	assert.Equal(t, "party:50055", config.Backends[0].Target)
}

func TestLoadConfig_DefaultPort(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
	})
	defer cleanup()

	config, err := LoadConfig()

	require.NoError(t, err)
	assert.Equal(t, 8080, config.Port)
}

func TestLoadConfig_LocalDevModeDefaultsFalse(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
	})
	defer cleanup()

	config, err := LoadConfig()

	require.NoError(t, err)
	assert.False(t, config.LocalDevMode)
}

func TestLoadConfig_RequiresBaseDomain(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"DATABASE_URL": "postgres://user@localhost/db",
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBaseDomainRequired)
}

func TestLoadConfig_RequiresDatabaseURL(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN": "api.example.com",
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDatabaseURLRequired)
}

func TestLoadConfig_InvalidBackendRoutesJSON(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":    "api.example.com",
		"DATABASE_URL":   "postgres://user@localhost/db",
		"BACKEND_ROUTES": "not valid json",
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidBackendsJSON)
}

func TestLoadConfig_EmptyBackendRoutes(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
	})
	defer cleanup()

	config, err := LoadConfig()

	require.NoError(t, err)
	assert.Empty(t, config.Backends)
}

func TestLoadConfig_MultipleBackendRoutes(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
		"BACKEND_ROUTES": `[
			{"prefix":"/v1/party","target":"party:50055"},
			{"prefix":"/v1/accounts","target":"current-account:50051"},
			{"prefix":"/v1/payments","target":"payment-order:50053"}
		]`,
	})
	defer cleanup()

	config, err := LoadConfig()

	require.NoError(t, err)
	require.Len(t, config.Backends, 3)
	assert.Equal(t, "/v1/party", config.Backends[0].Prefix)
	assert.Equal(t, "/v1/accounts", config.Backends[1].Prefix)
	assert.Equal(t, "/v1/payments", config.Backends[2].Prefix)
}

func TestLoadConfig_LocalDevModeVariations(t *testing.T) {
	testCases := []struct {
		name     string
		value    string
		expected bool
	}{
		{"true lowercase", "true", true},
		{"TRUE uppercase", "TRUE", true},
		{"1", "1", true},
		{"yes", "yes", true},
		{"false lowercase", "false", false},
		{"FALSE uppercase", "FALSE", false},
		{"0", "0", false},
		{"no", "no", false},
		{"invalid defaults to false", "invalid", false},
		{"empty defaults to false", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := setEnvVars(t, map[string]string{
				"BASE_DOMAIN":    "api.example.com",
				"DATABASE_URL":   "postgres://user@localhost/db",
				"LOCAL_DEV_MODE": tc.value,
			})
			defer cleanup()

			config, err := LoadConfig()

			require.NoError(t, err)
			assert.Equal(t, tc.expected, config.LocalDevMode)
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	testCases := []struct {
		name      string
		config    Config
		wantError error
	}{
		{
			name: "valid config",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
			},
			wantError: nil,
		},
		{
			name: "missing base domain",
			config: Config{
				Port:        8080,
				DatabaseURL: "postgres://localhost/db",
			},
			wantError: ErrBaseDomainRequired,
		},
		{
			name: "missing database URL",
			config: Config{
				Port:       8080,
				BaseDomain: "api.example.com",
			},
			wantError: ErrDatabaseURLRequired,
		},
		{
			name: "port too low",
			config: Config{
				Port:        0,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
			},
			wantError: ErrInvalidPort,
		},
		{
			name: "port too high",
			config: Config{
				Port:        65536,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
			},
			wantError: ErrInvalidPort,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()

			if tc.wantError != nil {
				assert.ErrorIs(t, err, tc.wantError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// setEnvVars sets environment variables and returns a cleanup function.
func setEnvVars(t *testing.T, vars map[string]string) func() {
	t.Helper()

	// Store original values
	originals := make(map[string]string)
	wasSet := make(map[string]bool)

	// Clear all gateway-related env vars first
	envsToClear := []string{
		"PORT", "BASE_DOMAIN", "LOCAL_DEV_MODE",
		"DATABASE_URL", "REDIS_URL", "BACKEND_ROUTES",
	}
	for _, key := range envsToClear {
		if val, ok := os.LookupEnv(key); ok {
			originals[key] = val
			wasSet[key] = true
		}
		os.Unsetenv(key)
	}

	// Set new values
	for key, value := range vars {
		if value != "" {
			os.Setenv(key, value)
		}
	}

	// Return cleanup function
	return func() {
		for _, key := range envsToClear {
			if wasSet[key] {
				os.Setenv(key, originals[key])
			} else {
				os.Unsetenv(key)
			}
		}
	}
}
