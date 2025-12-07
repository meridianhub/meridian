// Package clients provides gRPC client wrappers with resilience patterns.
//
// # Retry Logic
//
// The Retry function implements exponential backoff with jitter to handle transient failures.
// It works by wrapping any function that returns an error and automatically retrying it based
// on the configured policy.
//
// # Integration with Circuit Breaker
//
// Retry logic and circuit breaker can be composed in two ways:
//
// Option 1: Circuit Breaker wraps Retry (RECOMMENDED)
//
//	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("service"), logger)
//	err := cb.Execute(ctx, func() (any, error) {
//	    return nil, Retry(ctx, DefaultRetryConfig(), func() error {
//	        return grpcCall()
//	    })
//	})
//
// Benefits:
//   - Circuit opens after multiple retry attempts fail
//   - Prevents retry storms when service is down
//   - Circuit provides fast-fail when open
//
// Option 2: Retry wraps Circuit Breaker
//
//	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig("service"), logger)
//	err := Retry(ctx, DefaultRetryConfig(), func() error {
//	    _, err := cb.Execute(ctx, func() (any, error) {
//	        return grpcCall()
//	    })
//	    return err
//	})
//
// Benefits:
//   - Each retry attempt is circuit-protected
//   - Circuit opens faster on cascading failures
//   - Good for independent retry attempts
//
// Recommendation: Use Option 1 (Circuit Breaker wraps Retry) for most cases.
// This prevents overwhelming a failing service with retries and provides faster failure detection.
//
// This package re-exports types from shared/pkg/clients for backward compatibility.
// New code should import directly from github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	"context"

	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// RetryConfig is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type RetryConfig = sharedclients.RetryConfig

// DefaultRetryConfig returns a retry configuration with sensible defaults.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func DefaultRetryConfig() RetryConfig {
	return sharedclients.DefaultRetryConfig()
}

// NoRetryConfig returns a configuration that disables retries.
// Use this for non-idempotent operations.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func NoRetryConfig() RetryConfig {
	return sharedclients.NoRetryConfig()
}

// IsRetryable determines if an error should be retried.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func IsRetryable(err error) bool {
	return sharedclients.IsRetryable(err)
}

// Retry wraps a function with retry logic using exponential backoff with jitter.
// The function is retried if it returns a retryable error (see IsRetryable).
// Retry respects context cancellation and deadlines.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func Retry(ctx context.Context, config RetryConfig, fn func() error) error {
	return sharedclients.Retry(ctx, config, fn)
}
