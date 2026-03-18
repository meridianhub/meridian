package stripe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := Config{APIKey: "sk_test_key"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("empty API key", func(t *testing.T) {
		cfg := Config{}
		err := cfg.Validate()
		require.ErrorIs(t, err, ErrEmptyAPIKey)
	})
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 1000, cfg.TenantCacheSize)
	assert.NotZero(t, cfg.TenantCacheTTL)
	assert.NotEmpty(t, cfg.CircuitBreakerName)
	assert.NotZero(t, cfg.CircuitBreakerTimeout)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.NotZero(t, cfg.RetryInitialInterval)
	assert.NotZero(t, cfg.RetryMultiplier)

	// API key not set by default
	assert.Empty(t, cfg.APIKey)
	err := cfg.Validate()
	require.ErrorIs(t, err, ErrEmptyAPIKey)
}

func TestTenantConfig_Validate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		tc := TenantConfig{ConnectedAccountID: "acct_123"}
		require.NoError(t, tc.Validate())
	})

	t.Run("missing account ID", func(t *testing.T) {
		tc := TenantConfig{}
		err := tc.Validate()
		require.ErrorIs(t, err, ErrMissingAccountID)
	})
}
