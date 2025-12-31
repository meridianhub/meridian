// Package clients provides shared client resilience patterns including
// circuit breakers, retry logic, and saga orchestration for inter-service
// communication within the Meridian platform.
package clients

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/sony/gobreaker/v2"
)

// CircuitBreakerConfig holds configuration for circuit breaker
type CircuitBreakerConfig struct {
	Name          string
	MaxRequests   uint32
	Interval      time.Duration
	Timeout       time.Duration
	ReadyToTrip   func(counts gobreaker.Counts) bool
	OnStateChange func(name string, from gobreaker.State, to gobreaker.State)
}

// CircuitBreaker wraps sony/gobreaker with context support and logging
type CircuitBreaker struct {
	cb     *gobreaker.CircuitBreaker[any]
	logger *slog.Logger
}

// DefaultCircuitBreakerConfig returns a circuit breaker configuration with sensible defaults
func DefaultCircuitBreakerConfig(name string) CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:        name,
		MaxRequests: 1,
		Interval:    defaults.DefaultCircuitBreakerInterval,
		Timeout:     defaults.DefaultCircuitBreakerOpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip circuit after 5 consecutive failures
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: nil, // No default callback
	}
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(config CircuitBreakerConfig, logger *slog.Logger) *CircuitBreaker {
	if logger == nil {
		logger = slog.Default()
	}

	settings := gobreaker.Settings{
		Name:        config.Name,
		MaxRequests: config.MaxRequests,
		Interval:    config.Interval,
		Timeout:     config.Timeout,
		ReadyToTrip: config.ReadyToTrip,
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("circuit breaker state changed",
				"name", name,
				"from", from.String(),
				"to", to.String(),
			)
			if config.OnStateChange != nil {
				config.OnStateChange(name, from, to)
			}
		},
	}

	return &CircuitBreaker{
		cb:     gobreaker.NewCircuitBreaker[any](settings),
		logger: logger,
	}
}

// Execute wraps a function with circuit breaker protection and context awareness.
// It respects context cancellation and deadlines before executing the function.
//
// Goroutine behavior: When the context is cancelled, Execute returns immediately
// with the context error. However, the underlying function continues to run in a
// background goroutine until it completes. The goroutine will exit after fn()
// returns, but will not be interrupted. Callers should ensure that fn respects
// context cancellation if early termination is required.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() (any, error)) (any, error) {
	// Check context before executing
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context error before execution: %w", err)
	}

	// Create a channel to receive the result
	type result struct {
		value any
		err   error
	}
	resultChan := make(chan result, 1)

	// Execute the function with circuit breaker protection
	go func() {
		value, err := cb.cb.Execute(fn)
		resultChan <- result{value: value, err: err}
	}()

	// Wait for either the result or context cancellation
	select {
	case <-ctx.Done():
		// Context cancelled or timed out
		return nil, fmt.Errorf("context cancelled during execution: %w", ctx.Err())
	case res := <-resultChan:
		// Function completed
		return res.value, res.err
	}
}

// State returns the current state of the circuit breaker
func (cb *CircuitBreaker) State() gobreaker.State {
	return cb.cb.State()
}
