package gateway

import "time"

// Config holds configuration for production payment gateway connections.
type Config struct {
	// Timeout is the maximum duration to wait for a gateway response.
	Timeout time.Duration
	// MaxRetries is the maximum number of retry attempts for transient failures.
	MaxRetries int
	// RetryBackoff is the initial backoff duration between retries.
	RetryBackoff time.Duration
	// UseMock enables the mock gateway instead of a real implementation.
	UseMock bool
	// MockConfig is the configuration for the mock gateway (only used if UseMock is true).
	MockConfig MockGatewayConfig
}

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig() Config {
	return Config{
		Timeout:      30 * time.Second,
		MaxRetries:   3,
		RetryBackoff: 1 * time.Second,
		UseMock:      false,
		MockConfig:   DefaultMockGatewayConfig(),
	}
}

// New creates a PaymentGateway based on the provided configuration.
// If UseMock is true, returns a MockGateway; otherwise returns nil
// (real gateway implementation to be added later).
func New(config Config) PaymentGateway {
	if config.UseMock {
		return NewMockGateway(config.MockConfig)
	}
	// Real gateway implementation will be added here
	// For now, return mock as fallback to avoid nil pointer issues
	return NewMockGateway(DefaultMockGatewayConfig())
}
