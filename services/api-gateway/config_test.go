package gateway

import (
	"os"
	"testing"
	"time"

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
		"BACKENDS":       `[{"prefix":"/v1/party","target":"party:50055"}]`,
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
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
		"BACKENDS":     "not valid json",
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	// Verify errors.Is() correctly identifies the sentinel error.
	// This validates the fix for double %w wrapping which broke errors.Is().
	assert.ErrorIs(t, err, ErrInvalidBackendsJSON)
	// Also verify the underlying JSON error details are preserved in the message
	assert.Contains(t, err.Error(), "invalid character")
}

func TestLoadConfig_BackendRouteEmptyPrefix(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
		"BACKENDS":     `[{"prefix":"","target":"service:8080"}]`,
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidBackendRoute)
}

func TestLoadConfig_BackendRouteEmptyTarget(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
		"BACKENDS":     `[{"prefix":"/v1/api","target":""}]`,
	})
	defer cleanup()

	_, err := LoadConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidBackendRoute)
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
		"BACKENDS": `[
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
	// Note: shared/platform/env.GetEnvAsBool uses strconv.ParseBool which accepts:
	// true: "1", "t", "T", "true", "TRUE", "True"
	// false: "0", "f", "F", "false", "FALSE", "False"
	// It does NOT accept "yes"/"no" (those will fall back to default).
	testCases := []struct {
		name     string
		value    string
		expected bool
	}{
		{"true lowercase", "true", true},
		{"TRUE uppercase", "TRUE", true},
		{"True mixed case", "True", true},
		{"1", "1", true},
		{"t lowercase", "t", true},
		{"T uppercase", "T", true},
		{"false lowercase", "false", false},
		{"FALSE uppercase", "FALSE", false},
		{"False mixed case", "False", false},
		{"0", "0", false},
		{"f lowercase", "f", false},
		{"F uppercase", "F", false},
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
		{
			name: "backend route with empty prefix",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Backends:    []BackendRoute{{Prefix: "", Target: "service:8080"}},
			},
			wantError: ErrInvalidBackendRoute,
		},
		{
			name: "backend route with empty target",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Backends:    []BackendRoute{{Prefix: "/v1/api", Target: ""}},
			},
			wantError: ErrInvalidBackendRoute,
		},
		{
			name: "valid backend routes",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Backends:    []BackendRoute{{Prefix: "/v1/api", Target: "service:8080"}},
			},
			wantError: nil,
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

func TestConfig_ValidateForNamespace(t *testing.T) {
	testCases := []struct {
		name         string
		localDevMode bool
		namespace    string
		wantError    bool
	}{
		// LOCAL_DEV_MODE=true tests
		{
			name:         "LOCAL_DEV_MODE=true in production namespace",
			localDevMode: true,
			namespace:    "production",
			wantError:    true,
		},
		{
			name:         "LOCAL_DEV_MODE=true in prod namespace",
			localDevMode: true,
			namespace:    "prod",
			wantError:    true,
		},
		{
			name:         "LOCAL_DEV_MODE=true in prod-eu namespace",
			localDevMode: true,
			namespace:    "prod-eu",
			wantError:    true,
		},
		{
			name:         "LOCAL_DEV_MODE=true in prod-us namespace",
			localDevMode: true,
			namespace:    "prod-us",
			wantError:    true,
		},
		{
			name:         "LOCAL_DEV_MODE=true in staging namespace",
			localDevMode: true,
			namespace:    "staging",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=true in development namespace",
			localDevMode: true,
			namespace:    "development",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=true in dev namespace",
			localDevMode: true,
			namespace:    "dev",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=true with empty namespace",
			localDevMode: true,
			namespace:    "",
			wantError:    false,
		},
		// LOCAL_DEV_MODE=false tests (should always pass)
		{
			name:         "LOCAL_DEV_MODE=false in production namespace",
			localDevMode: false,
			namespace:    "production",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=false in prod namespace",
			localDevMode: false,
			namespace:    "prod",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=false in staging namespace",
			localDevMode: false,
			namespace:    "staging",
			wantError:    false,
		},
		{
			name:         "LOCAL_DEV_MODE=false with empty namespace",
			localDevMode: false,
			namespace:    "",
			wantError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &Config{
				Port:         8080,
				BaseDomain:   "api.example.com",
				DatabaseURL:  "postgres://localhost/db",
				LocalDevMode: tc.localDevMode,
			}

			err := config.ValidateForNamespace(tc.namespace)

			if tc.wantError {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrLocalDevModeInProduction)
				assert.Contains(t, err.Error(), tc.namespace)
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
		"DATABASE_URL", "REDIS_URL", "BACKENDS",
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

func TestConfig_Validate_AuthEnabled(t *testing.T) {
	testCases := []struct {
		name      string
		config    Config
		wantError error
	}{
		{
			name: "auth enabled without JWKS URL fails",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Auth: AuthConfig{
					Enabled: true,
					JWKSURL: "",
				},
			},
			wantError: ErrJWKSURLRequired,
		},
		{
			name: "auth enabled with JWKS URL succeeds",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Auth: AuthConfig{
					Enabled: true,
					JWKSURL: "https://auth.example.com/.well-known/jwks.json",
				},
			},
			wantError: nil,
		},
		{
			name: "auth disabled without JWKS URL succeeds",
			config: Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/db",
				Auth: AuthConfig{
					Enabled: false,
					JWKSURL: "",
				},
			},
			wantError: nil,
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

func TestLoadAuthConfig_Defaults(t *testing.T) {
	// Clear all auth-related env vars (including DEX_ISSUER which can set defaults)
	authEnvVars := []string{
		"AUTH_ENABLED", "JWKS_URL", "JWKS_CACHE_TTL", "JWKS_REFRESH_TTL",
		"JWT_ISSUER", "JWT_AUDIENCE", "API_KEYS",
		"API_KEY_RATE_LIMIT_PER_SECOND", "API_KEY_RATE_LIMIT_BURST",
		"DEX_ISSUER",
	}
	for _, key := range authEnvVars {
		os.Unsetenv(key)
	}

	config := LoadAuthConfig()

	assert.False(t, config.Enabled)
	assert.Empty(t, config.JWKSURL)
	assert.Equal(t, 24*time.Hour, config.JWKSCacheTTL)
	assert.Equal(t, 1*time.Hour, config.JWKSRefreshTTL)
	assert.Empty(t, config.Issuer)
	assert.Empty(t, config.Audience)
	assert.Nil(t, config.APIKeys)
	assert.Equal(t, float64(100), config.RateLimitPerSecond)
	assert.Equal(t, 200, config.RateLimitBurst)
}

func TestLoadAuthConfig_FullConfiguration(t *testing.T) {
	// Set all auth-related env vars
	cleanup := setAuthEnvVars(t, map[string]string{
		"AUTH_ENABLED":                  "true",
		"JWKS_URL":                      "https://auth.example.com/.well-known/jwks.json",
		"JWKS_CACHE_TTL":                "12h",
		"JWKS_REFRESH_TTL":              "30m",
		"JWT_ISSUER":                    "https://auth.example.com",
		"JWT_AUDIENCE":                  "api.example.com",
		"API_KEYS":                      "key1:service-a,key2:service-b",
		"API_KEY_RATE_LIMIT_PER_SECOND": "50",
		"API_KEY_RATE_LIMIT_BURST":      "100",
	})
	defer cleanup()

	config := LoadAuthConfig()

	assert.True(t, config.Enabled)
	assert.Equal(t, "https://auth.example.com/.well-known/jwks.json", config.JWKSURL)
	assert.Equal(t, 12*time.Hour, config.JWKSCacheTTL)
	assert.Equal(t, 30*time.Minute, config.JWKSRefreshTTL)
	assert.Equal(t, "https://auth.example.com", config.Issuer)
	assert.Equal(t, "api.example.com", config.Audience)
	require.Len(t, config.APIKeys, 2)
	assert.Equal(t, "service-a", config.APIKeys["key1"])
	assert.Equal(t, "service-b", config.APIKeys["key2"])
	assert.Equal(t, float64(50), config.RateLimitPerSecond)
	assert.Equal(t, 100, config.RateLimitBurst)
}

func TestParseAPIKeysEnv(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: map[string]string{},
		},
		{
			name:     "single key",
			input:    "key1:identity1",
			expected: map[string]string{"key1": "identity1"},
		},
		{
			name:     "multiple keys",
			input:    "key1:identity1,key2:identity2,key3:identity3",
			expected: map[string]string{"key1": "identity1", "key2": "identity2", "key3": "identity3"},
		},
		{
			name:     "with spaces",
			input:    " key1 : identity1 , key2 : identity2 ",
			expected: map[string]string{"key1": "identity1", "key2": "identity2"},
		},
		{
			name:     "ignores invalid entries",
			input:    "key1:identity1,invalid,key2:identity2",
			expected: map[string]string{"key1": "identity1", "key2": "identity2"},
		},
		{
			name:     "ignores empty key or identity",
			input:    "key1:identity1,:identity2,key3:",
			expected: map[string]string{"key1": "identity1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := parseAPIKeysEnv(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestLoadEventStreamConfig_Defaults verifies default values when no env vars are set.
func TestLoadEventStreamConfig_Defaults(t *testing.T) {
	eventStreamEnvVars := []string{
		"EVENT_STREAM_ENABLED", "KAFKA_ENABLED", "KAFKA_BROKERS", "KAFKA_TOPICS",
		"OUTBOX_POLL_INTERVAL", "EVENT_STREAM_REDIS_ENABLED",
		"EVENT_STREAM_MAX_CONNECTIONS", "EVENT_STREAM_BUFFER_SIZE",
	}
	for _, key := range eventStreamEnvVars {
		os.Unsetenv(key)
	}

	cfg := loadEventStreamConfig()

	assert.False(t, cfg.Enabled)
	assert.False(t, cfg.KafkaEnabled)
	assert.Empty(t, cfg.KafkaBrokers)
	assert.Nil(t, cfg.KafkaTopics)
	assert.Equal(t, 500*time.Millisecond, cfg.OutboxPollInterval)
	assert.False(t, cfg.RedisEnabled)
	assert.Equal(t, 0, cfg.MaxConnections)
	assert.Equal(t, 256, cfg.BufferSize)
}

// TestLoadEventStreamConfig_FullConfiguration verifies all env vars are loaded.
func TestLoadEventStreamConfig_FullConfiguration(t *testing.T) {
	cleanup := setEventStreamEnvVars(t, map[string]string{
		"EVENT_STREAM_ENABLED":         "true",
		"KAFKA_ENABLED":                "true",
		"KAFKA_BROKERS":                "kafka1:9092,kafka2:9092",
		"KAFKA_TOPICS":                 "events.payment,events.party",
		"OUTBOX_POLL_INTERVAL":         "1s",
		"EVENT_STREAM_REDIS_ENABLED":   "true",
		"EVENT_STREAM_MAX_CONNECTIONS": "500",
		"EVENT_STREAM_BUFFER_SIZE":     "512",
	})
	defer cleanup()

	cfg := loadEventStreamConfig()

	assert.True(t, cfg.Enabled)
	assert.True(t, cfg.KafkaEnabled)
	assert.Equal(t, "kafka1:9092,kafka2:9092", cfg.KafkaBrokers)
	assert.Equal(t, []string{"events.payment", "events.party"}, cfg.KafkaTopics)
	assert.Equal(t, 1*time.Second, cfg.OutboxPollInterval)
	assert.True(t, cfg.RedisEnabled)
	assert.Equal(t, 500, cfg.MaxConnections)
	assert.Equal(t, 512, cfg.BufferSize)
}

// TestLoadConfig_EventStreamDefaults verifies EventStreamConfig is included in LoadConfig output.
func TestLoadConfig_EventStreamDefaults(t *testing.T) {
	cleanup := setEnvVars(t, map[string]string{
		"BASE_DOMAIN":  "api.example.com",
		"DATABASE_URL": "postgres://user@localhost/db",
	})
	defer cleanup()

	// Clear event stream env vars
	eventStreamEnvVars := []string{
		"EVENT_STREAM_ENABLED", "KAFKA_ENABLED", "KAFKA_BROKERS", "KAFKA_TOPICS",
		"OUTBOX_POLL_INTERVAL", "EVENT_STREAM_REDIS_ENABLED",
		"EVENT_STREAM_MAX_CONNECTIONS", "EVENT_STREAM_BUFFER_SIZE",
	}
	for _, key := range eventStreamEnvVars {
		os.Unsetenv(key)
	}

	config, err := LoadConfig()

	require.NoError(t, err)
	assert.False(t, config.EventStream.Enabled)
	assert.False(t, config.EventStream.KafkaEnabled)
	assert.Equal(t, 500*time.Millisecond, config.EventStream.OutboxPollInterval)
	assert.Equal(t, 256, config.EventStream.BufferSize)
}

// TestValidate_KafkaValidation verifies that Kafka-specific fields are validated when Kafka is enabled.
func TestValidate_KafkaValidation(t *testing.T) {
	t.Run("kafka_enabled_no_brokers", func(t *testing.T) {
		cfg := &Config{
			BaseDomain:  "api.example.com",
			DatabaseURL: "postgres://user@localhost/db",
			Port:        8080,
			EventStream: EventStreamConfig{
				Enabled:      true,
				KafkaEnabled: true,
				KafkaTopics:  []string{"events"},
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, ErrKafkaBrokersRequired)
	})

	t.Run("kafka_enabled_no_topics", func(t *testing.T) {
		cfg := &Config{
			BaseDomain:  "api.example.com",
			DatabaseURL: "postgres://user@localhost/db",
			Port:        8080,
			EventStream: EventStreamConfig{
				Enabled:      true,
				KafkaEnabled: true,
				KafkaBrokers: "kafka:9092",
			},
		}
		err := cfg.Validate()
		assert.ErrorIs(t, err, ErrKafkaTopicsRequired)
	})

	t.Run("kafka_enabled_with_all_fields", func(t *testing.T) {
		cfg := &Config{
			BaseDomain:  "api.example.com",
			DatabaseURL: "postgres://user@localhost/db",
			Port:        8080,
			EventStream: EventStreamConfig{
				Enabled:      true,
				KafkaEnabled: true,
				KafkaBrokers: "kafka:9092",
				KafkaTopics:  []string{"events"},
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})

	t.Run("kafka_fields_not_validated_when_disabled", func(t *testing.T) {
		cfg := &Config{
			BaseDomain:  "api.example.com",
			DatabaseURL: "postgres://user@localhost/db",
			Port:        8080,
			EventStream: EventStreamConfig{
				Enabled:      true,
				KafkaEnabled: false, // Outbox mode - Kafka fields not required
			},
		}
		err := cfg.Validate()
		assert.NoError(t, err)
	})
}

// setEventStreamEnvVars sets event-stream-related environment variables and returns a cleanup function.
func setEventStreamEnvVars(t *testing.T, vars map[string]string) func() {
	t.Helper()

	eventStreamEnvVars := []string{
		"EVENT_STREAM_ENABLED", "KAFKA_ENABLED", "KAFKA_BROKERS", "KAFKA_TOPICS",
		"OUTBOX_POLL_INTERVAL", "EVENT_STREAM_REDIS_ENABLED",
		"EVENT_STREAM_MAX_CONNECTIONS", "EVENT_STREAM_BUFFER_SIZE",
	}

	originals := make(map[string]string)
	wasSet := make(map[string]bool)

	for _, key := range eventStreamEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			originals[key] = val
			wasSet[key] = true
		}
		os.Unsetenv(key)
	}

	for key, value := range vars {
		os.Setenv(key, value)
	}

	return func() {
		for _, key := range eventStreamEnvVars {
			if wasSet[key] {
				os.Setenv(key, originals[key])
			} else {
				os.Unsetenv(key)
			}
		}
	}
}

// TestLoadAuthConfig_DexIssuerDefaults verifies that DEX_ISSUER sets sensible
// defaults for JWKS_URL and JWT_ISSUER when they are not explicitly set.
func TestLoadAuthConfig_DexIssuerDefaults(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantJWKSURL string
		wantIssuer  string
	}{
		{
			name:        "no DEX_ISSUER, no JWKS_URL — both empty",
			env:         map[string]string{},
			wantJWKSURL: "",
			wantIssuer:  "",
		},
		{
			name: "DEX_ISSUER set, JWKS_URL not set — defaults to Dex keys endpoint",
			env: map[string]string{
				"DEX_ISSUER": "http://localhost:8090/dex",
			},
			wantJWKSURL: "http://localhost:8090/dex/keys",
			wantIssuer:  "http://localhost:8090/dex",
		},
		{
			name: "DEX_ISSUER set, JWKS_URL explicitly set — explicit wins",
			env: map[string]string{
				"DEX_ISSUER": "http://localhost:8090/dex",
				"JWKS_URL":   "https://custom.example.com/.well-known/jwks.json",
			},
			wantJWKSURL: "https://custom.example.com/.well-known/jwks.json",
			wantIssuer:  "http://localhost:8090/dex",
		},
		{
			name: "DEX_ISSUER set, JWT_ISSUER explicitly set — explicit wins",
			env: map[string]string{
				"DEX_ISSUER": "http://localhost:8090/dex",
				"JWT_ISSUER": "https://auth.example.com",
			},
			wantJWKSURL: "http://localhost:8090/dex/keys",
			wantIssuer:  "https://auth.example.com",
		},
		{
			name: "DEX_ISSUER with trailing slash — normalised",
			env: map[string]string{
				"DEX_ISSUER": "http://localhost:8090/dex/",
			},
			wantJWKSURL: "http://localhost:8090/dex/keys",
			wantIssuer:  "http://localhost:8090/dex",
		},
		{
			name: "all explicitly set — DEX_ISSUER has no effect",
			env: map[string]string{
				"DEX_ISSUER": "http://localhost:8090/dex",
				"JWKS_URL":   "https://custom.example.com/jwks",
				"JWT_ISSUER": "https://auth.example.com",
			},
			wantJWKSURL: "https://custom.example.com/jwks",
			wantIssuer:  "https://auth.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all auth+dex env vars for isolation
			for _, k := range []string{"DEX_ISSUER", "JWKS_URL", "JWT_ISSUER", "JWT_AUDIENCE", "AUTH_ENABLED", "API_KEYS"} {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			config := LoadAuthConfig()

			assert.Equal(t, tt.wantJWKSURL, config.JWKSURL, "JWKSURL")
			assert.Equal(t, tt.wantIssuer, config.Issuer, "Issuer")
		})
	}
}

func setAuthEnvVars(t *testing.T, vars map[string]string) func() {
	t.Helper()

	authEnvVars := []string{
		"AUTH_ENABLED", "JWKS_URL", "JWKS_CACHE_TTL", "JWKS_REFRESH_TTL",
		"JWT_ISSUER", "JWT_AUDIENCE", "API_KEYS",
		"API_KEY_RATE_LIMIT_PER_SECOND", "API_KEY_RATE_LIMIT_BURST",
		"DEX_ISSUER",
	}

	// Store original values
	originals := make(map[string]string)
	wasSet := make(map[string]bool)

	for _, key := range authEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			originals[key] = val
			wasSet[key] = true
		}
		os.Unsetenv(key)
	}

	// Set new values
	for key, value := range vars {
		os.Setenv(key, value)
	}

	// Return cleanup function
	return func() {
		for _, key := range authEnvVars {
			if wasSet[key] {
				os.Setenv(key, originals[key])
			} else {
				os.Unsetenv(key)
			}
		}
	}
}
