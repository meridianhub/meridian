// Package verification provides KYC/AML verification capabilities for party onboarding.
package verification

import (
	"errors"
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
//
// Future providers (stubs, not yet implemented):
//   - "jumio": Jumio identity verification
//   - "onfido": Onfido identity verification
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
	case "jumio":
		// Jumio provider not yet implemented
		return nil, ErrUnsupportedProvider
	case "onfido":
		// Onfido provider not yet implemented
		return nil, ErrUnsupportedProvider
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
	case "jumio":
		return nil, ErrUnsupportedProvider
	case "onfido":
		return nil, ErrUnsupportedProvider
	default:
		return nil, ErrUnsupportedProvider
	}
}
