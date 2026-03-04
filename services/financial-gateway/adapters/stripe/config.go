// Package stripe provides a tenant-scoped Stripe Connect client wrapper
// with Connected Account routing, circuit breaker, and retry resilience
// for the financial-gateway service.
package stripe

import (
	"errors"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

// Config holds the Stripe gateway configuration.
type Config struct {
	// APIKey is the platform Stripe API key (from STRIPE_SECRET_KEY env var).
	APIKey string

	// TenantCacheSize is the max number of tenant configs to cache.
	TenantCacheSize int

	// TenantCacheTTL is how long tenant configs are cached before refresh.
	TenantCacheTTL time.Duration

	// CircuitBreakerName identifies this circuit breaker in logs.
	CircuitBreakerName string

	// CircuitBreakerTimeout is how long the breaker stays open before half-open.
	CircuitBreakerTimeout time.Duration

	// CircuitBreakerInterval is the cyclic period for clearing counts in closed state.
	CircuitBreakerInterval time.Duration

	// CircuitBreakerMaxRequests is the max requests allowed in half-open state.
	CircuitBreakerMaxRequests uint32

	// CircuitBreakerFailureThreshold is consecutive failures to trip the breaker.
	CircuitBreakerFailureThreshold uint32

	// MaxRetries is the maximum number of retry attempts for transient failures.
	MaxRetries int

	// RetryInitialInterval is the starting backoff delay.
	RetryInitialInterval time.Duration

	// RetryMaxInterval caps exponential backoff growth.
	RetryMaxInterval time.Duration

	// RetryMultiplier is the exponential backoff multiplier.
	RetryMultiplier float64

	// RetryRandomizationFactor adds jitter to backoff timing.
	RetryRandomizationFactor float64
}

// Configuration errors.
var (
	ErrEmptyAPIKey = errors.New("stripe API key must not be empty")
)

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig() Config {
	return Config{
		TenantCacheSize: 1000,
		TenantCacheTTL:  5 * time.Minute,

		CircuitBreakerName:             "stripe-financial-gateway",
		CircuitBreakerTimeout:          defaults.DefaultCircuitBreakerOpenTimeout,
		CircuitBreakerInterval:         defaults.DefaultCircuitBreakerInterval,
		CircuitBreakerMaxRequests:      1,
		CircuitBreakerFailureThreshold: 5,

		MaxRetries:               3,
		RetryInitialInterval:     500 * time.Millisecond,
		RetryMaxInterval:         defaults.DefaultMaxRetryInterval,
		RetryMultiplier:          2.0,
		RetryRandomizationFactor: 0.5,
	}
}

// Validate checks that required fields are set.
func (c Config) Validate() error {
	if c.APIKey == "" {
		return ErrEmptyAPIKey
	}
	return nil
}
