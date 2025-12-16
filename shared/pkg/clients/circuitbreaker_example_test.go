package clients_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/sony/gobreaker/v2"
)

var (
	errServiceUnavailable = errors.New("service unavailable")
	errServiceError       = errors.New("service error")
)

// ExampleNewCircuitBreaker demonstrates creating a circuit breaker with default configuration.
func ExampleNewCircuitBreaker() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	config := clients.DefaultCircuitBreakerConfig("my-service")
	cb := clients.NewCircuitBreaker(config, logger)

	fmt.Printf("Circuit breaker created, state: %s\n", cb.State())
	// Output: Circuit breaker created, state: closed
}

// ExampleCircuitBreaker_Execute demonstrates basic circuit breaker usage.
func ExampleCircuitBreaker_Execute() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	config := clients.DefaultCircuitBreakerConfig("example-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute operation with circuit breaker protection
	result, err := cb.Execute(ctx, func() (any, error) {
		// Your downstream service call here
		return "success", nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %v\n", result)
	// Output: Result: success
}

// ExampleDefaultCircuitBreakerConfig demonstrates the default configuration values.
func ExampleDefaultCircuitBreakerConfig() {
	config := clients.DefaultCircuitBreakerConfig("my-service")

	fmt.Printf("Name: %s\n", config.Name)
	fmt.Printf("MaxRequests: %d\n", config.MaxRequests)
	fmt.Printf("Interval: %v\n", config.Interval)
	fmt.Printf("Timeout: %v\n", config.Timeout)
	// Output:
	// Name: my-service
	// MaxRequests: 1
	// Interval: 1m0s
	// Timeout: 30s
}

// ExampleCircuitBreaker_Execute_withFallback demonstrates fallback logic when circuit is open.
func ExampleCircuitBreaker_Execute_withFallback() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Configure circuit to trip after 2 failures with short timeout
	config := clients.CircuitBreakerConfig{
		Name:        "fallback-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Simulate service call with fallback
	executeWithFallback := func() (string, error) {
		result, err := cb.Execute(ctx, func() (any, error) {
			return nil, errServiceUnavailable
		})
		if err != nil {
			// Check if circuit is open
			if errors.Is(err, gobreaker.ErrOpenState) {
				return "cached-data", nil
			}
			return "", err
		}
		return result.(string), nil
	}

	// First two calls fail and trip the circuit
	_, _ = executeWithFallback()
	_, _ = executeWithFallback()

	// Third call returns cached data because circuit is open
	result, _ := executeWithFallback()
	fmt.Printf("Result: %s\n", result)
	// Output: Result: cached-data
}

// ExampleCircuitBreaker_Execute_customConfig demonstrates custom circuit breaker configuration.
func ExampleCircuitBreaker_Execute_customConfig() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Custom configuration with aggressive circuit breaking
	config := clients.CircuitBreakerConfig{
		Name:        "aggressive-service",
		MaxRequests: 3,                 // Allow 3 requests in half-open state
		Interval:    120 * time.Second, // Reset counts every 2 minutes
		Timeout:     45 * time.Second,  // Stay open for 45 seconds
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip after 3 consecutive failures
			return counts.ConsecutiveFailures >= 3
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			// Log state changes for monitoring
			fmt.Printf("Circuit %s: %s -> %s\n", name, from, to)
		},
	}

	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Execute operation
	_, err := cb.Execute(ctx, func() (any, error) {
		return "success", nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("State after success: %s\n", cb.State())
	// Output: State after success: closed
}

// ExampleCircuitBreaker_State demonstrates monitoring circuit breaker state.
func ExampleCircuitBreaker_State() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	config := clients.DefaultCircuitBreakerConfig("monitored-service")
	cb := clients.NewCircuitBreaker(config, logger)

	// Check current state
	state := cb.State()

	switch state {
	case gobreaker.StateClosed:
		fmt.Println("Circuit is closed - normal operation")
	case gobreaker.StateOpen:
		fmt.Println("Circuit is open - service is unhealthy")
	case gobreaker.StateHalfOpen:
		fmt.Println("Circuit is half-open - testing recovery")
	}
	// Output: Circuit is closed - normal operation
}

// ExampleCircuitBreaker_Execute_errorHandling demonstrates comprehensive error handling.
func ExampleCircuitBreaker_Execute_errorHandling() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	config := clients.DefaultCircuitBreakerConfig("error-handling-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute with comprehensive error handling
	_, err := cb.Execute(ctx, func() (any, error) {
		return nil, errServiceError
	})
	if err != nil {
		switch {
		case errors.Is(err, gobreaker.ErrOpenState):
			fmt.Println("Service unavailable - using fallback")
		case errors.Is(err, gobreaker.ErrTooManyRequests):
			fmt.Println("Service busy - retry later")
		case errors.Is(err, context.DeadlineExceeded):
			fmt.Println("Request timeout")
		default:
			fmt.Printf("Service error: %v\n", err)
		}
	}
	// Output: Service error: service error
}
