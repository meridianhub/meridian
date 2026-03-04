package gateway

import (
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

// Provider constants for gateway selection.
const (
	ProviderStripe           = "stripe"
	ProviderMock             = "mock"
	ProviderFinancialGateway = "financial-gateway"
)

// Config holds configuration for payment gateway connections.
// Note: Timeout, MaxRetries, and RetryBackoff are defined for the real gateway
// implementation (to be added). The mock gateway does not use these values.
type Config struct {
	// Provider selects the gateway implementation: "stripe" or "mock".
	// Empty defaults to mock. Note: New() does not use Provider; it is
	// consumed by the service entry point factory (cmd/main.go).
	Provider string
	// StripeAPIKey is the platform-level Stripe API key.
	// Required when Provider is "stripe".
	StripeAPIKey string
	// Timeout is the maximum duration to wait for a gateway response.
	// Used by real gateway implementation (not mock).
	Timeout time.Duration
	// MaxRetries is the maximum number of retry attempts for transient failures.
	// Used by real gateway implementation (not mock).
	MaxRetries int
	// RetryBackoff is the initial backoff duration between retries.
	// Used by real gateway implementation (not mock).
	RetryBackoff time.Duration
	// UseMock enables the mock gateway instead of a real implementation.
	UseMock bool
	// MockConfig is the configuration for the mock gateway (only used if UseMock is true).
	MockConfig MockGatewayConfig
}

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig() Config {
	return Config{
		Timeout:      defaults.DefaultRPCTimeout,
		MaxRetries:   3,
		RetryBackoff: 1 * time.Second,
		UseMock:      false,
		MockConfig:   DefaultMockGatewayConfig(),
	}
}

// New creates a PaymentGateway based on the provided configuration.
// If UseMock is true, returns a MockGateway with the provided MockConfig.
// Otherwise, returns a MockGateway with default config as a fallback
// until the real gateway implementation is added.
//
// For provider-based instantiation (e.g., Stripe), construct the adapter
// directly in the service entry point (cmd/main.go) to avoid import cycles.
func New(config Config) PaymentGateway {
	if config.UseMock {
		return NewMockGateway(config.MockConfig)
	}
	// Real gateway implementation will be added here
	// For now, return mock as fallback to avoid nil pointer issues
	return NewMockGateway(DefaultMockGatewayConfig())
}
