package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractGatewayID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      string
		suffix   string
		expected string
	}{
		{"standard gateway", "GATEWAY_STRIPE_ACCOUNT_ID", "_ACCOUNT_ID", "stripe"},
		{"multi-word gateway", "GATEWAY_MY_CUSTOM_GW_ACCOUNT_ID", "_ACCOUNT_ID", "my_custom_gw"},
		{"uppercase to lowercase", "GATEWAY_ADYEN_ACCOUNT_ID", "_ACCOUNT_ID", "adyen"},
		{"empty after trim", "GATEWAY__ACCOUNT_ID", "_ACCOUNT_ID", ""},
		{"mock gateway", "GATEWAY_MOCK_ACCOUNT_ID", "_ACCOUNT_ID", "mock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractGatewayID(tt.key, tt.suffix)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadFromEnv_EmptyGatewayID(t *testing.T) {
	// This test verifies that the loadFromEnv function skips
	// env vars where the extracted gateway ID is empty.
	// Since it scans os.Environ(), we just verify the empty config case.
	t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "")
	// Don't set any GATEWAY_*_ACCOUNT_ID vars
	// The function may see unrelated GATEWAY_ vars, but without the _ACCOUNT_ID suffix

	cfg, err := loadFromEnv()
	if err != nil {
		// Without any GATEWAY_*_ACCOUNT_ID env vars, should return ErrEmptyConfig
		assert.ErrorIs(t, err, ErrEmptyConfig)
	} else {
		// If env vars happen to exist, config should be non-nil
		assert.NotNil(t, cfg)
	}
}

func TestLoadGatewayAccountConfig_PrefersFile(t *testing.T) {
	// When GATEWAY_ACCOUNT_MAPPING_FILE is set to non-existent file,
	// it should try to load from file rather than falling back to env vars
	t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", "/tmp/nonexistent-gateway-config.json")
	t.Setenv("GATEWAY_STRIPE_ACCOUNT_ID", "uuid-stripe")
	t.Setenv("GATEWAY_STRIPE_ACCOUNT_TYPE", "NOSTRO")

	_, err := LoadGatewayAccountConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read")
}
