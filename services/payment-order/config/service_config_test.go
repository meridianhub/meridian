package config

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Validation tests ---

func TestValidate_MockProviderDefaults(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderMock,
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_StripeProviderValid(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "sk_test_abc123",
		StripeWebhookSecret:    "whsec_test_abc123",
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_StripeMissingAPIKey(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "",
		StripeWebhookSecret:    "whsec_test_abc123",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStripeAPIKey)
}

func TestValidate_StripeMissingWebhookSecret(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "sk_test_abc123",
		StripeWebhookSecret:    "",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStripeWebhookSecret)
}

func TestValidate_FinancialGatewayProviderValid(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderFinancialGateway,
		FinancialGatewayAddr:   "financial-gateway:50064",
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_FinancialGatewayMissingAddr(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderFinancialGateway,
		FinancialGatewayAddr:   "",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingFinancialGatewayAddr)
}

func TestValidate_UnsupportedProvider(t *testing.T) {
	cfg := ServiceConfig{
		PaymentGatewayProvider: "paypal",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidGatewayProvider)
}

func TestValidate_EmptyProviderDefaultsToMock(t *testing.T) {
	// When LoadServiceConfig is used, empty env var defaults to "mock".
	// But if someone constructs ServiceConfig with empty string directly, it
	// should fail as unsupported.
	cfg := ServiceConfig{
		PaymentGatewayProvider: "",
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidGatewayProvider)
}

// --- LoadServiceConfig tests ---

func TestLoadServiceConfig_Defaults(t *testing.T) {
	// Clear all relevant env vars to test defaults.
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "")
	t.Setenv("STRIPE_API_KEY", "")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("BILLING_ENABLED", "")
	t.Setenv("BILLING_CRON_SCHEDULE", "")
	t.Setenv("BILLING_SHADOW_MODE", "")
	t.Setenv("DUNNING_POLL_INTERVAL", "")

	cfg := LoadServiceConfig()

	assert.Equal(t, gateway.ProviderMock, cfg.PaymentGatewayProvider)
	assert.Empty(t, cfg.StripeAPIKey)
	assert.Empty(t, cfg.StripeWebhookSecret)
	assert.False(t, cfg.BillingEnabled)
	assert.Equal(t, "0 0 * * *", cfg.BillingCronSchedule)
	assert.False(t, cfg.BillingShadowMode)
	assert.Equal(t, 5*time.Minute, cfg.DunningPollInterval)
}

func TestLoadServiceConfig_StripeFromEnv(t *testing.T) {
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "stripe")
	t.Setenv("STRIPE_API_KEY", "sk_live_key123")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_secret456")
	t.Setenv("BILLING_ENABLED", "true")
	t.Setenv("BILLING_CRON_SCHEDULE", "*/5 * * * *")
	t.Setenv("BILLING_SHADOW_MODE", "true")
	t.Setenv("DUNNING_POLL_INTERVAL", "10m")

	cfg := LoadServiceConfig()

	assert.Equal(t, gateway.ProviderStripe, cfg.PaymentGatewayProvider)
	assert.Equal(t, "sk_live_key123", cfg.StripeAPIKey)
	assert.Equal(t, "whsec_secret456", cfg.StripeWebhookSecret)
	assert.True(t, cfg.BillingEnabled)
	assert.Equal(t, "*/5 * * * *", cfg.BillingCronSchedule)
	assert.True(t, cfg.BillingShadowMode)
	assert.Equal(t, 10*time.Minute, cfg.DunningPollInterval)
}

func TestLoadServiceConfig_ValidateIntegration(t *testing.T) {
	// Stripe provider without API key should fail validation.
	t.Setenv("PAYMENT_GATEWAY_PROVIDER", "stripe")
	t.Setenv("STRIPE_API_KEY", "")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")

	cfg := LoadServiceConfig()
	err := cfg.Validate()

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStripeAPIKey)
}

// --- Redaction tests ---

func TestRedact_Empty(t *testing.T) {
	assert.Equal(t, "(not set)", redact(""))
}

func TestRedact_Short(t *testing.T) {
	// Strings shorter than 12 chars are fully masked.
	assert.Equal(t, "***********", redact("short_value"))
}

func TestRedact_Long(t *testing.T) {
	// "sk_test_abc123def456" is 20 chars => first 4 + 12 stars + last 4
	result := redact("sk_test_abc123def456")
	assert.Equal(t, "sk_t************f456", result)
	// Verify original value is not present.
	assert.NotContains(t, result, "abc123def")
}

func TestRedact_Exactly12(t *testing.T) {
	// 12 chars: first 4 + 4 stars + last 4
	result := redact("abcdefghijkl")
	assert.Equal(t, "abcd****ijkl", result)
}

// --- LogValues test ---

func TestLogValues_RedactsSensitiveValues(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	cfg := ServiceConfig{
		PaymentGatewayProvider: gateway.ProviderStripe,
		StripeAPIKey:           "sk_live_realkey123456",
		StripeWebhookSecret:    "whsec_realsecret7890",
		BillingEnabled:         true,
		BillingCronSchedule:    "0 0 * * *",
		BillingShadowMode:      false,
		DunningPollInterval:    5 * time.Minute,
	}

	cfg.LogValues(logger)

	output := buf.String()
	// Sensitive values should NOT appear in full.
	assert.NotContains(t, output, "sk_live_realkey123456")
	assert.NotContains(t, output, "whsec_realsecret7890")
	// Redacted form should appear.
	assert.Contains(t, output, "sk_l")
	assert.Contains(t, output, "whse")
	// Non-sensitive values should appear.
	assert.Contains(t, output, "stripe")
	assert.Contains(t, output, "0 0 * * *")
}
