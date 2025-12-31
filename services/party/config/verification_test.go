package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearVerificationEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"VERIFICATION_PROVIDER",
		"VERIFICATION_WEBHOOK_SECRET",
		"VERIFICATION_WEBHOOK_URL",
		"VERIFICATION_API_KEY",
		"VERIFICATION_API_SECRET",
		"VERIFICATION_BASE_URL",
	}
	for _, key := range envVars {
		_ = os.Unsetenv(key)
	}
}

func TestLoadVerificationConfig_MockProvider_MinimalConfig(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "mock")

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Provider)
	assert.Empty(t, cfg.WebhookSecret)
	assert.Empty(t, cfg.WebhookURL)
}

func TestLoadVerificationConfig_MockProvider_WithOptionalFields(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "mock")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "optional-secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://example.com/webhook")

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Provider)
	assert.Equal(t, "optional-secret", cfg.WebhookSecret)
	assert.Equal(t, "https://example.com/webhook", cfg.WebhookURL)
}

func TestLoadVerificationConfig_NonMockProvider_RequiresWebhookSecret(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "jumio")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("VERIFICATION_API_KEY", "api-key")
	t.Setenv("VERIFICATION_API_SECRET", "api-secret")
	// Missing VERIFICATION_WEBHOOK_SECRET

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyWebhookSecretForNonMock)
}

func TestLoadVerificationConfig_NonMockProvider_RequiresWebhookURL(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "jumio")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "secret")
	// Missing VERIFICATION_WEBHOOK_URL
	t.Setenv("VERIFICATION_API_KEY", "api-key")
	t.Setenv("VERIFICATION_API_SECRET", "api-secret")

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyWebhookURLForNonMock)
}

func TestLoadVerificationConfig_NonMockProvider_RequiresAPIKey(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "onfido")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://example.com/webhook")
	// Missing VERIFICATION_API_KEY
	t.Setenv("VERIFICATION_API_SECRET", "api-secret")

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingProviderAPIKey)
}

func TestLoadVerificationConfig_NonMockProvider_RequiresAPISecret(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "onfido")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://example.com/webhook")
	t.Setenv("VERIFICATION_API_KEY", "api-key")
	// Missing VERIFICATION_API_SECRET

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingProviderAPISecret)
}

func TestLoadVerificationConfig_NonMockProvider_ValidFullConfig(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "jumio")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://api.example.com/webhooks/verification")
	t.Setenv("VERIFICATION_API_KEY", "my-api-key")
	t.Setenv("VERIFICATION_API_SECRET", "my-api-secret")
	t.Setenv("VERIFICATION_BASE_URL", "https://custom.jumio.com/api")

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "jumio", cfg.Provider)
	assert.Equal(t, "webhook-secret", cfg.WebhookSecret)
	assert.Equal(t, "https://api.example.com/webhooks/verification", cfg.WebhookURL)
	assert.Equal(t, "my-api-key", cfg.ProviderConfig["api_key"])
	assert.Equal(t, "my-api-secret", cfg.ProviderConfig["api_secret"])
	assert.Equal(t, "https://custom.jumio.com/api", cfg.ProviderConfig["base_url"])
}

func TestLoadVerificationConfig_MissingProvider(t *testing.T) {
	clearVerificationEnv(t)
	// No VERIFICATION_PROVIDER set

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyProvider)
}

func TestLoadVerificationConfig_UnsupportedProvider(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "unknown-provider")

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestLoadVerificationConfig_TrimsWhitespace(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "  mock  ")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "  secret  ")

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "mock", cfg.Provider)
	assert.Equal(t, "secret", cfg.WebhookSecret)
}

func TestLoadVerificationConfig_WhitespaceOnlyProvider(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "   \t\n   ")

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyProvider)
}

func TestVerificationConfig_Validate_EmptyProvider(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "",
	}

	err := cfg.Validate()

	assert.ErrorIs(t, err, ErrEmptyProvider)
}

func TestVerificationConfig_Validate_UnsupportedProvider(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "invalid-provider",
	}

	err := cfg.Validate()

	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestVerificationConfig_Validate_MockProviderNoExtraRequirements(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "mock",
		// No webhook secret or URL required
	}

	err := cfg.Validate()

	assert.NoError(t, err)
}

func TestVerificationConfig_Validate_ProviderCaseInsensitive(t *testing.T) {
	testCases := []struct {
		provider string
	}{
		{"mock"},
		{"MOCK"},
		{"Mock"},
		{"jumio"},
		{"JUMIO"},
		{"Jumio"},
		{"onfido"},
		{"ONFIDO"},
		{"Onfido"},
	}

	for _, tc := range testCases {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := &VerificationConfig{
				Provider:       tc.provider,
				WebhookSecret:  "secret",
				WebhookURL:     "https://example.com/webhook",
				ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
			}

			err := cfg.Validate()

			// For mock, this should pass without webhook config
			// For non-mock, with full config, this should pass
			if cfg.IsMock() {
				assert.NoError(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVerificationConfig_IsMock(t *testing.T) {
	testCases := []struct {
		provider string
		expected bool
	}{
		{"mock", true},
		{"MOCK", true},
		{"Mock", true},
		{"jumio", false},
		{"onfido", false},
		{"", false},
	}

	for _, tc := range testCases {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := &VerificationConfig{Provider: tc.provider}
			assert.Equal(t, tc.expected, cfg.IsMock())
		})
	}
}

func TestVerificationConfig_SupportedProviders(t *testing.T) {
	// Verify the SupportedProviders list contains expected values
	assert.Contains(t, SupportedProviders, "mock")
	assert.Contains(t, SupportedProviders, "jumio")
	assert.Contains(t, SupportedProviders, "onfido")
	assert.Len(t, SupportedProviders, 3)
}
