// Package verification provides KYC/AML verification capabilities for party onboarding.
package verification

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/meridianhub/meridian/services/party/config"
)

// Factory errors
var (
	ErrNilConfig           = errors.New("verification config cannot be nil")
	ErrUnsupportedProvider = errors.New("unsupported verification provider")
)

// NewProvider creates a new verification provider based on the configuration.
//
// Currently supported providers:
//   - "mock": Returns a MockProvider for testing and development
//   - "onfido": Onfido identity verification
//   - "stripe": Stripe Identity verification
//
// Returns ErrUnsupportedProvider for any unrecognized provider name.
// The MockProvider is configured with AlwaysApprove=true by default.
func NewProvider(cfg *config.VerificationConfig) (Provider, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	provider := strings.ToLower(cfg.Provider)
	switch provider {
	case "mock":
		return NewMockProvider().WithAlwaysApprove(true), nil
	case "onfido":
		return NewOnfidoProvider(cfg, slog.Default())
	case "stripe":
		return NewStripeIdentityProvider(cfg, slog.Default())
	default:
		return nil, ErrUnsupportedProvider
	}
}

// ProviderOptions contains additional options for provider creation.
// This is useful for testing where you want to configure the MockProvider behavior.
type ProviderOptions struct {
	// MockAlwaysApprove controls whether the mock provider approves all verifications.
	MockAlwaysApprove bool
	// MockAsyncMode enables async simulation in the mock provider.
	MockAsyncMode bool
}

// DefaultProviderOptions returns the default provider options.
func DefaultProviderOptions() ProviderOptions {
	return ProviderOptions{
		MockAlwaysApprove: true,
		MockAsyncMode:     false,
	}
}

// NewProviderWithOptions creates a new verification provider with custom options.
func NewProviderWithOptions(cfg *config.VerificationConfig, opts ProviderOptions) (Provider, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}

	provider := strings.ToLower(cfg.Provider)
	switch provider {
	case "mock":
		return NewMockProvider().
			WithAlwaysApprove(opts.MockAlwaysApprove).
			WithAsyncMode(opts.MockAsyncMode), nil
	case "onfido":
		return NewOnfidoProvider(cfg, slog.Default())
	case "stripe":
		return NewStripeIdentityProvider(cfg, slog.Default())
	default:
		return nil, ErrUnsupportedProvider
	}
}
