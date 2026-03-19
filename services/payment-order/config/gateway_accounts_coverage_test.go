package config

import (
	"errors"
	"path/filepath"
	"strings"
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

func TestLoadFromEnv_NoGatewayEnvVars(t *testing.T) {
	// loadFromEnv scans os.Environ(), so the result depends on the test environment.
	// We just verify the function doesn't panic and returns a valid result.
	_, err := loadFromEnv()
	// Either succeeds (env vars found) or returns ErrEmptyConfig (none found)
	// or returns a validation error (found but invalid)
	if err != nil {
		assert.True(t,
			errors.Is(err, ErrEmptyConfig) || strings.Contains(err.Error(), "invalid"),
			"unexpected error: %v", err)
	}
}

func TestLoadGatewayAccountConfig_PrefersFile(t *testing.T) {
	// When GATEWAY_ACCOUNT_MAPPING_FILE is set to non-existent file,
	// it should try to load from file rather than falling back to env vars
	t.Setenv("GATEWAY_ACCOUNT_MAPPING_FILE", filepath.Join(t.TempDir(), "nonexistent-gateway-config.json"))
	t.Setenv("GATEWAY_STRIPE_ACCOUNT_ID", "uuid-stripe")
	t.Setenv("GATEWAY_STRIPE_ACCOUNT_TYPE", "NOSTRO")

	_, err := LoadGatewayAccountConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read")
}
