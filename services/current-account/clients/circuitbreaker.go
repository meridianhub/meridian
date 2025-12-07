// Package clients provides gRPC client wrappers with resilience patterns.
//
// This package re-exports types from shared/pkg/clients for backward compatibility.
// New code should import directly from github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/sony/gobreaker/v2"
)

// CircuitBreakerConfig is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type CircuitBreakerConfig = sharedclients.CircuitBreakerConfig

// CircuitBreaker is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type CircuitBreaker = sharedclients.CircuitBreaker

// DefaultCircuitBreakerConfig returns a circuit breaker configuration with sensible defaults.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return sharedclients.DefaultCircuitBreakerConfig(name)
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
var NewCircuitBreaker = sharedclients.NewCircuitBreaker

// Re-export gobreaker types for convenience
type (
	// State is the circuit breaker state type from gobreaker.
	State = gobreaker.State
	// Counts holds circuit breaker statistics.
	Counts = gobreaker.Counts
)

// Circuit breaker states
const (
	StateClosed   = gobreaker.StateClosed
	StateHalfOpen = gobreaker.StateHalfOpen
	StateOpen     = gobreaker.StateOpen
)

// Circuit breaker errors
var (
	ErrOpenState       = gobreaker.ErrOpenState
	ErrTooManyRequests = gobreaker.ErrTooManyRequests
)
