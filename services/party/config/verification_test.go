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
		"STRIPE_WEBHOOK_SECRET",
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
	t.Setenv("VERIFICATION_PROVIDER", "onfido")
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
	t.Setenv("VERIFICATION_PROVIDER", "onfido")
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
	t.Setenv("VERIFICATION_PROVIDER", "onfido")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://api.example.com/webhooks/verification")
	t.Setenv("VERIFICATION_API_KEY", "my-api-key")
	t.Setenv("VERIFICATION_API_SECRET", "my-api-secret")
	t.Setenv("VERIFICATION_BASE_URL", "https://custom.onfido.com/api")

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "onfido", cfg.Provider)
	assert.Equal(t, "webhook-secret", cfg.WebhookSecret)
	assert.Equal(t, "https://api.example.com/webhooks/verification", cfg.WebhookURL)
	assert.Equal(t, "my-api-key", cfg.ProviderConfig["api_key"])
	assert.Equal(t, "my-api-secret", cfg.ProviderConfig["api_secret"])
	assert.Equal(t, "https://custom.onfido.com/api", cfg.ProviderConfig["base_url"])
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
		{"onfido", false},
		{"stripe", false},
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
	assert.Contains(t, SupportedProviders, "onfido")
	assert.Contains(t, SupportedProviders, "stripe")
	assert.Len(t, SupportedProviders, 3)
}

func TestLoadVerificationConfig_StripeProvider_OnlyNeedsAPIKey(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "stripe")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://api.example.com/webhooks/verification")
	t.Setenv("VERIFICATION_API_KEY", "sk_test_my-stripe-key")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test_stripe_endpoint_secret")
	// No VERIFICATION_API_SECRET — Stripe does not need it

	cfg, err := LoadVerificationConfig()

	require.NoError(t, err)
	assert.Equal(t, "stripe", cfg.Provider)
	assert.Equal(t, "sk_test_my-stripe-key", cfg.ProviderConfig["api_key"])
	assert.Empty(t, cfg.ProviderConfig["api_secret"])
	assert.Equal(t, "whsec_test_stripe_endpoint_secret", cfg.StripeWebhookSecret)
}

func TestLoadVerificationConfig_StripeProvider_MissingStripeWebhookSecret(t *testing.T) {
	clearVerificationEnv(t)
	t.Setenv("VERIFICATION_PROVIDER", "stripe")
	t.Setenv("VERIFICATION_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("VERIFICATION_WEBHOOK_URL", "https://api.example.com/webhooks/verification")
	t.Setenv("VERIFICATION_API_KEY", "sk_test_my-stripe-key")
	// No STRIPE_WEBHOOK_SECRET — required for Stripe

	_, err := LoadVerificationConfig()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStripeWebhookSecret)
}

func TestVerificationConfig_Validate_StripeRequiresWebhookConfig(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:       "stripe",
		ProviderConfig: map[string]string{"api_key": "sk_test_key"},
		// Missing WebhookSecret and WebhookURL
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyWebhookSecretForNonMock)
}

func TestVerificationConfig_Validate_StripeWithoutAPISecretPasses(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:            "stripe",
		WebhookSecret:       "webhook-secret",
		StripeWebhookSecret: "whsec_test_endpoint_secret",
		WebhookURL:          "https://example.com/webhooks/verification",
		ProviderConfig:      map[string]string{"api_key": "sk_test_key"},
	}

	err := cfg.Validate()

	assert.NoError(t, err)
}

func TestVerificationConfig_Validate_StripeMissingStripeWebhookSecret(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:            "stripe",
		WebhookSecret:       "webhook-secret",
		StripeWebhookSecret: "", // missing
		WebhookURL:          "https://example.com/webhooks/verification",
		ProviderConfig:      map[string]string{"api_key": "sk_test_key"},
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStripeWebhookSecret)
}

func TestVerificationConfig_ValidateForEnvironment_ProductionRejectsMock(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "mock",
	}

	err := cfg.ValidateForEnvironment("production")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMockProviderInProduction)
}

func TestVerificationConfig_ValidateForEnvironment_ProductionRejectsHTTPWebhook(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:       "onfido",
		WebhookSecret:  "a]strongsecretthatis32charslong!!", // 32+ chars
		WebhookURL:     "http://api.example.com/webhooks",   // HTTP, not HTTPS
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	err := cfg.ValidateForEnvironment("production")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWebhookHTTPSRequired)
}

func TestVerificationConfig_ValidateForEnvironment_ProductionRejectsWeakSecret(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:       "onfido",
		WebhookSecret:  "short-secret", // < 32 chars
		WebhookURL:     "https://api.example.com/webhooks",
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	err := cfg.ValidateForEnvironment("production")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrWebhookSecretTooShort)
}

func TestVerificationConfig_ValidateForEnvironment_DevelopmentAllowsMock(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "mock",
	}

	err := cfg.ValidateForEnvironment("development")

	assert.NoError(t, err)
}

func TestVerificationConfig_ValidateForEnvironment_ProductionAcceptsValidConfig(t *testing.T) {
	cfg := &VerificationConfig{
		Provider:       "onfido",
		WebhookSecret:  "a-very-strong-secret-that-is-at-least-32-characters-long",
		WebhookURL:     "https://api.example.com/webhooks/verification",
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	err := cfg.ValidateForEnvironment("production")

	assert.NoError(t, err)
}

func TestVerificationConfig_ValidateForEnvironment_ProdShorthandAlsoEnforces(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "mock",
	}

	err := cfg.ValidateForEnvironment("prod")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMockProviderInProduction)
}

func TestVerificationConfig_ValidateForEnvironment_RunsBaseValidateFirst(t *testing.T) {
	cfg := &VerificationConfig{
		Provider: "", // empty - should fail base Validate()
	}

	err := cfg.ValidateForEnvironment("production")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyProvider)
}
