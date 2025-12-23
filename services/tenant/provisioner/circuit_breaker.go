// Package provisioner provides PostgreSQL schema provisioning for multi-tenant isolation.
package provisioner

import (
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"
)

// Circuit breaker configuration constants.
const (
	// BreakerMaxRequests is the maximum number of requests allowed in half-open state
	// before the breaker decides whether to transition to open or closed state.
	BreakerMaxRequests uint32 = 3

	// BreakerInterval is the cyclic period of the closed state for clearing
	// internal Counts. If 0, the internal Counts are never cleared while in closed state.
	BreakerInterval = 60 * time.Second

	// BreakerTimeout is the period of the open state, after which the circuit breaker
	// transitions to half-open state to allow test requests through.
	BreakerTimeout = 300 * time.Second // 5 minutes

	// BreakerMinRequests is the minimum number of requests needed before the
	// failure ratio is evaluated for tripping the breaker.
	BreakerMinRequests uint32 = 5

	// BreakerFailureRatio is the failure percentage that triggers the open state.
	// 0.6 = 60% failure rate.
	BreakerFailureRatio = 0.6
)

// ServiceCircuitBreakers manages per-service circuit breakers with thread-safe access.
// Each service database gets its own circuit breaker to prevent repeated provisioning
// attempts when a specific service is consistently failing.
type ServiceCircuitBreakers struct {
	breakers map[string]*gobreaker.CircuitBreaker[any]
	mu       sync.RWMutex
}

// NewServiceCircuitBreakers creates a new ServiceCircuitBreakers instance.
func NewServiceCircuitBreakers() *ServiceCircuitBreakers {
	return &ServiceCircuitBreakers{
		breakers: make(map[string]*gobreaker.CircuitBreaker[any]),
	}
}

// GetBreaker returns the circuit breaker for a given service name.
// If no breaker exists for the service, a new one is created with the
// standard configuration. This method uses double-checked locking for
// thread-safe lazy initialization.
func (s *ServiceCircuitBreakers) GetBreaker(serviceName string) *gobreaker.CircuitBreaker[any] {
	// Fast path: check if breaker already exists with read lock
	s.mu.RLock()
	breaker, exists := s.breakers[serviceName]
	s.mu.RUnlock()

	if exists {
		return breaker
	}

	// Slow path: acquire write lock and create breaker
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check: another goroutine may have created the breaker
	// while we were waiting for the write lock
	if breaker, exists = s.breakers[serviceName]; exists {
		return breaker
	}

	// Create new breaker with standard configuration
	breaker = gobreaker.NewCircuitBreaker[any](gobreaker.Settings{
		Name:        serviceName,
		MaxRequests: BreakerMaxRequests,
		Interval:    BreakerInterval,
		Timeout:     BreakerTimeout,
		ReadyToTrip: readyToTrip,
	})

	s.breakers[serviceName] = breaker
	return breaker
}

// readyToTrip determines whether the circuit breaker should trip to open state.
// It requires at least BreakerMinRequests (5) requests and a failure ratio
// of at least BreakerFailureRatio (60%) to trip.
func readyToTrip(counts gobreaker.Counts) bool {
	if counts.Requests < BreakerMinRequests {
		return false
	}
	failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
	return failureRatio >= BreakerFailureRatio
}
