package stripe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate_MissingAPIKey(t *testing.T) {
	cfg := DefaultConfig()
	// APIKey not set
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyAPIKey)
}

func TestConfig_Validate_WithAPIKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIKey = "sk_test_123"
	err := cfg.Validate()
	require.NoError(t, err)
}

func TestDefaultConfig_HasReasonableDefaults(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 1000, cfg.TenantCacheSize)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Greater(t, cfg.RetryInitialInterval.Milliseconds(), int64(0))
	assert.Greater(t, cfg.CircuitBreakerFailureThreshold, uint32(0))
}

func TestTenantConfig_Validate_MissingAccountID(t *testing.T) {
	tc := TenantConfig{}
	err := tc.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAccountID)
}

func TestTenantConfig_Validate_WithAccountID(t *testing.T) {
	tc := TenantConfig{ConnectedAccountID: "acct_123"}
	err := tc.Validate()
	require.NoError(t, err)
}
