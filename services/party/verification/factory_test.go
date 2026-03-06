package verification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/party/config"
)

func TestNewProvider_NilConfig(t *testing.T) {
	provider, err := NewProvider(nil)

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrNilConfig)
}

func TestNewProvider_MockProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "mock",
	}

	provider, err := NewProvider(cfg)

	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify it's a MockProvider
	mockProvider, ok := provider.(*MockProvider)
	assert.True(t, ok, "expected *MockProvider type")
	assert.True(t, mockProvider.AlwaysApprove, "mock provider should default to AlwaysApprove=true")
}

func TestNewProvider_MockProviderCaseInsensitive(t *testing.T) {
	testCases := []string{"mock", "MOCK", "Mock", "mOcK"}

	for _, providerName := range testCases {
		t.Run(providerName, func(t *testing.T) {
			cfg := &config.VerificationConfig{
				Provider: providerName,
			}

			provider, err := NewProvider(cfg)

			require.NoError(t, err)
			require.NotNil(t, provider)
		})
	}
}

func TestNewProvider_OnfidoProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "onfido",
		WebhookSecret:  "secret",
		WebhookURL:     "https://example.com/webhook",
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	provider, err := NewProvider(cfg)

	require.NoError(t, err)
	require.NotNil(t, provider)

	_, ok := provider.(*OnfidoProvider)
	assert.True(t, ok, "expected *OnfidoProvider type")
}

func TestNewProvider_UnknownProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "unknown-provider",
	}

	provider, err := NewProvider(cfg)

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrUnsupportedProvider)
}

func TestNewProviderWithOptions_NilConfig(t *testing.T) {
	provider, err := NewProviderWithOptions(nil, DefaultProviderOptions())

	assert.Nil(t, provider)
	assert.ErrorIs(t, err, ErrNilConfig)
}

func TestNewProviderWithOptions_MockProviderWithOptions(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider: "mock",
	}
	opts := ProviderOptions{
		MockAlwaysApprove: false,
		MockAsyncMode:     true,
	}

	provider, err := NewProviderWithOptions(cfg, opts)

	require.NoError(t, err)
	require.NotNil(t, provider)

	mockProvider, ok := provider.(*MockProvider)
	require.True(t, ok)
	assert.False(t, mockProvider.AlwaysApprove)
	assert.True(t, mockProvider.AsyncMode)
}

func TestDefaultProviderOptions(t *testing.T) {
	opts := DefaultProviderOptions()

	assert.True(t, opts.MockAlwaysApprove)
	assert.False(t, opts.MockAsyncMode)
}

func TestNewProviderWithOptions_RespectsAlwaysApprove(t *testing.T) {
	testCases := []struct {
		name           string
		alwaysApprove  bool
		expectedResult bool
	}{
		{"AlwaysApprove true", true, true},
		{"AlwaysApprove false", false, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.VerificationConfig{Provider: "mock"}
			opts := ProviderOptions{MockAlwaysApprove: tc.alwaysApprove}

			provider, err := NewProviderWithOptions(cfg, opts)

			require.NoError(t, err)
			mockProvider := provider.(*MockProvider)
			assert.Equal(t, tc.expectedResult, mockProvider.AlwaysApprove)
		})
	}
}

func TestNewProviderWithOptions_RespectsAsyncMode(t *testing.T) {
	testCases := []struct {
		name           string
		asyncMode      bool
		expectedResult bool
	}{
		{"AsyncMode true", true, true},
		{"AsyncMode false", false, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.VerificationConfig{Provider: "mock"}
			opts := ProviderOptions{MockAsyncMode: tc.asyncMode}

			provider, err := NewProviderWithOptions(cfg, opts)

			require.NoError(t, err)
			mockProvider := provider.(*MockProvider)
			assert.Equal(t, tc.expectedResult, mockProvider.AsyncMode)
		})
	}
}

func TestNewProviderWithOptions_NonMockProvider_ReturnsError(t *testing.T) {
	testCases := []string{"unknown", "nonexistent"}

	for _, providerName := range testCases {
		t.Run(providerName, func(t *testing.T) {
			cfg := &config.VerificationConfig{
				Provider:       providerName,
				WebhookSecret:  "secret",
				WebhookURL:     "https://example.com/webhook",
				ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
			}

			provider, err := NewProviderWithOptions(cfg, DefaultProviderOptions())

			assert.Nil(t, provider)
			assert.ErrorIs(t, err, ErrUnsupportedProvider)
		})
	}
}

func TestNewProvider_StripeProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:            "stripe",
		WebhookSecret:       "webhook-secret",
		StripeWebhookSecret: "whsec_test_endpoint_secret",
		WebhookURL:          "https://example.com/webhook",
		ProviderConfig:      map[string]string{"api_key": "sk_test_key"},
	}

	provider, err := NewProvider(cfg)

	require.NoError(t, err)
	require.NotNil(t, provider)

	_, ok := provider.(*StripeIdentityProvider)
	assert.True(t, ok, "expected *StripeIdentityProvider type")
}

func TestNewProviderWithOptions_StripeProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:            "stripe",
		WebhookSecret:       "webhook-secret",
		StripeWebhookSecret: "whsec_test_endpoint_secret",
		WebhookURL:          "https://example.com/webhook",
		ProviderConfig:      map[string]string{"api_key": "sk_test_key"},
	}

	provider, err := NewProviderWithOptions(cfg, DefaultProviderOptions())

	require.NoError(t, err)
	require.NotNil(t, provider)

	_, ok := provider.(*StripeIdentityProvider)
	assert.True(t, ok, "expected *StripeIdentityProvider type")
}

func TestNewProviderWithOptions_OnfidoProvider(t *testing.T) {
	cfg := &config.VerificationConfig{
		Provider:       "onfido",
		WebhookSecret:  "secret",
		WebhookURL:     "https://example.com/webhook",
		ProviderConfig: map[string]string{"api_key": "key", "api_secret": "secret"},
	}

	provider, err := NewProviderWithOptions(cfg, DefaultProviderOptions())

	require.NoError(t, err)
	require.NotNil(t, provider)

	_, ok := provider.(*OnfidoProvider)
	assert.True(t, ok, "expected *OnfidoProvider type")
}
