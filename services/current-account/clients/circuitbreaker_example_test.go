package clients_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/sony/gobreaker/v2"
)

const (
	successString = "success"
)

var (
	errServiceUnavailable = errors.New("service unavailable")
	errServiceError       = errors.New("service error")
)

// ExampleCircuitBreaker_basic demonstrates basic circuit breaker usage
func ExampleCircuitBreaker_basic() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("example-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute operation with circuit breaker protection
	result, err := cb.Execute(ctx, func() (any, error) {
		// Your downstream service call here
		return successString, nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %v\n", result)
	// Output: Result: success
}

// ExampleCircuitBreaker_withFallback demonstrates fallback logic when circuit is open
func ExampleCircuitBreaker_withFallback() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("example-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Simulate service call
	executeServiceCall := func() (string, error) {
		result, err := cb.Execute(ctx, func() (any, error) {
			// Simulate downstream service call
			return nil, errServiceUnavailable
		})
		if err != nil {
			// Check if circuit is open
			if errors.Is(err, gobreaker.ErrOpenState) {
				// Provide fallback response
				return "cached-data", nil
			}
			return "", fmt.Errorf("service call failed: %w", err)
		}

		return result.(string), nil
	}

	result, err := executeServiceCall()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %v\n", result)
}

// ExampleCircuitBreaker_customConfig demonstrates custom configuration
func ExampleCircuitBreaker_customConfig() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Custom configuration for aggressive circuit breaking
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
			fmt.Printf("Circuit %s changed: %s -> %s\n", name, from, to)
		},
	}

	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Execute operation
	_, err := cb.Execute(ctx, func() (any, error) {
		return successString, nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Operation completed with state: %s\n", cb.State())
	// Output: Operation completed with state: closed
}

// ExampleCircuitBreaker_stateMonitoring demonstrates monitoring circuit state
func ExampleCircuitBreaker_stateMonitoring() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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
	default:
		fmt.Println("Circuit is in unknown state")
	}

	// Output: Circuit is closed - normal operation
}

// ExampleCircuitBreaker_errorHandling demonstrates comprehensive error handling
func ExampleCircuitBreaker_errorHandling() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("error-handling-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute with comprehensive error handling
	executeWithErrorHandling := func() error {
		_, err := cb.Execute(ctx, func() (any, error) {
			// Simulate service call
			return nil, errServiceError
		})
		if err != nil {
			switch {
			case errors.Is(err, gobreaker.ErrOpenState):
				// Circuit is open - use cached data or degraded mode
				fmt.Println("Service unavailable - using fallback")
				return nil
			case errors.Is(err, gobreaker.ErrTooManyRequests):
				// Too many requests in half-open state
				fmt.Println("Service busy - retry later")
				return fmt.Errorf("service busy: %w", err)
			case errors.Is(err, context.DeadlineExceeded):
				// Context timeout
				fmt.Println("Request timeout")
				return fmt.Errorf("request timeout: %w", err)
			default:
				// Other service error
				fmt.Printf("Service error: %v\n", err)
				return fmt.Errorf("service error: %w", err)
			}
		}

		return nil
	}

	_ = executeWithErrorHandling()
	// Output: Service error: service error
}
