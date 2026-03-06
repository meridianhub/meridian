// Package config provides configuration structures for the party service.
package config

import (
	"errors"
	"os"
	"strings"
)

// VerificationConfig holds configuration for KYC/AML verification providers.
//
// Provider identifies which verification provider to use. Supported values:
//   - "mock": Mock provider for testing (always available)
//   - "onfido": Onfido identity verification
//   - "stripe": Stripe Identity verification
//
// ProviderConfig contains provider-specific settings like API keys and endpoints.
// Required keys vary by provider.
//
// WebhookSecret is the HMAC secret used to verify incoming webhook signatures
// for the generic webhook handler and as the inner HMAC secret for the Stripe
// adapter. This prevents malicious actors from spoofing verification callbacks.
//
// StripeWebhookSecret is the Stripe endpoint signing secret (whsec_ prefixed)
// used exclusively to validate the Stripe-Signature header on inbound Stripe
// webhooks. Required when provider is "stripe".
//
// WebhookURL is the publicly accessible URL where providers send verification
// callbacks. Required for production deployments.
type VerificationConfig struct {
	// Provider is the verification provider to use ("mock", "onfido", "stripe").
	Provider string

	// ProviderConfig contains provider-specific configuration.
	// For onfido: "api_key", "api_secret", "base_url" (optional).
	// For stripe: "api_key", "base_url" (optional), "stripe_account" (optional).
	ProviderConfig map[string]string

	// WebhookSecret is the HMAC secret for validating webhook signatures on the
	// generic handler and as the inner HMAC secret for the Stripe adapter.
	WebhookSecret string

	// StripeWebhookSecret is the Stripe endpoint signing secret (whsec_ prefixed)
	// used to validate inbound Stripe webhook signatures.
	// Loaded from STRIPE_WEBHOOK_SECRET. Required when provider is "stripe".
	StripeWebhookSecret string

	// WebhookURL is the public URL for provider callbacks (e.g., "https://api.example.com/webhooks/verification").
	WebhookURL string
}

// Validation errors
var (
	ErrEmptyProvider                = errors.New("verification provider is required")
	ErrUnsupportedProvider          = errors.New("unsupported verification provider")
	ErrEmptyWebhookSecretForNonMock = errors.New("webhook secret is required for non-mock providers")
	ErrEmptyWebhookURLForNonMock    = errors.New("webhook URL is required for non-mock providers")
	ErrMissingProviderAPIKey        = errors.New("api_key is required in provider config")
	ErrMissingProviderAPISecret     = errors.New("api_secret is required in provider config")
	ErrMissingStripeWebhookSecret   = errors.New("stripe_webhook_secret is required when provider is stripe (set STRIPE_WEBHOOK_SECRET)")
	ErrMockProviderInProduction     = errors.New("mock provider not allowed in production")
	ErrWebhookHTTPSRequired         = errors.New("webhook URL must use HTTPS in production")
	ErrWebhookSecretTooShort        = errors.New("webhook secret must be at least 32 characters in production")
)

// SupportedProviders lists all supported verification provider names.
var SupportedProviders = []string{"mock", "onfido", "stripe"}

// LoadVerificationConfig loads verification configuration from environment variables.
//
// Required environment variables:
//   - VERIFICATION_PROVIDER: The provider to use (required)
//
// Conditional environment variables (required for non-mock providers):
//   - VERIFICATION_WEBHOOK_SECRET: HMAC secret for generic webhook validation
//   - VERIFICATION_WEBHOOK_URL: Public webhook callback URL
//
// Provider-specific environment variables:
//   - VERIFICATION_API_KEY: Provider API key
//   - VERIFICATION_API_SECRET: Provider API secret (not required for Stripe)
//   - VERIFICATION_BASE_URL: Provider base URL (optional, provider default used if not set)
//
// Stripe-specific environment variables:
//   - STRIPE_WEBHOOK_SECRET: Stripe endpoint signing secret (whsec_ prefixed).
//     Required when VERIFICATION_PROVIDER=stripe.
func LoadVerificationConfig() (*VerificationConfig, error) {
	providerConfig := make(map[string]string)

	// Load optional provider-specific config
	if apiKey := strings.TrimSpace(os.Getenv("VERIFICATION_API_KEY")); apiKey != "" {
		providerConfig["api_key"] = apiKey
	}
	if apiSecret := strings.TrimSpace(os.Getenv("VERIFICATION_API_SECRET")); apiSecret != "" {
		providerConfig["api_secret"] = apiSecret
	}
	if baseURL := strings.TrimSpace(os.Getenv("VERIFICATION_BASE_URL")); baseURL != "" {
		providerConfig["base_url"] = baseURL
	}

	cfg := &VerificationConfig{
		Provider:            strings.TrimSpace(os.Getenv("VERIFICATION_PROVIDER")),
		ProviderConfig:      providerConfig,
		WebhookSecret:       strings.TrimSpace(os.Getenv("VERIFICATION_WEBHOOK_SECRET")),
		StripeWebhookSecret: strings.TrimSpace(os.Getenv("STRIPE_WEBHOOK_SECRET")),
		WebhookURL:          strings.TrimSpace(os.Getenv("VERIFICATION_WEBHOOK_URL")),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate validates the verification configuration.
func (c *VerificationConfig) Validate() error {
	if c.Provider == "" {
		return ErrEmptyProvider
	}

	// Check if provider is supported
	if !c.isSupportedProvider() {
		return ErrUnsupportedProvider
	}

	// Mock provider has relaxed requirements
	if c.Provider == "mock" {
		return nil
	}

	// Non-mock providers require webhook configuration
	if c.WebhookSecret == "" {
		return ErrEmptyWebhookSecretForNonMock
	}
	if c.WebhookURL == "" {
		return ErrEmptyWebhookURLForNonMock
	}

	// Non-mock providers require API credentials
	if c.ProviderConfig["api_key"] == "" {
		return ErrMissingProviderAPIKey
	}
	// Stripe only needs api_key; other providers require api_secret as well
	if strings.ToLower(c.Provider) != "stripe" && c.ProviderConfig["api_secret"] == "" {
		return ErrMissingProviderAPISecret
	}

	// Stripe requires its own endpoint signing secret (distinct from the generic HMAC secret)
	if strings.ToLower(c.Provider) == "stripe" && c.StripeWebhookSecret == "" {
		return ErrMissingStripeWebhookSecret
	}

	return nil
}

// isSupportedProvider checks if the configured provider is in the supported list.
func (c *VerificationConfig) isSupportedProvider() bool {
	provider := strings.ToLower(c.Provider)
	for _, supported := range SupportedProviders {
		if provider == supported {
			return true
		}
	}
	return false
}

// ValidateForEnvironment validates the configuration with additional
// constraints based on the deployment environment. In production:
//   - Mock provider is not allowed
//   - Webhook URL must use HTTPS
//   - Webhook secret must be at least 32 characters
func (c *VerificationConfig) ValidateForEnvironment(environment string) error {
	if err := c.Validate(); err != nil {
		return err
	}

	if strings.ToLower(environment) == "production" || strings.ToLower(environment) == "prod" {
		if c.IsMock() {
			return ErrMockProviderInProduction
		}
		if !strings.HasPrefix(strings.ToLower(c.WebhookURL), "https://") {
			return ErrWebhookHTTPSRequired
		}
		if len(c.WebhookSecret) < 32 {
			return ErrWebhookSecretTooShort
		}
	}

	return nil
}

// IsMock returns true if the mock provider is configured.
func (c *VerificationConfig) IsMock() bool {
	return strings.ToLower(c.Provider) == "mock"
}
