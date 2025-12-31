package clients

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/sony/gobreaker/v2"
)

// ErrTypeAssertion is returned when a type assertion fails in ExecuteWithResilience
var ErrTypeAssertion = errors.New("type assertion failed")

// ResilientClientConfig holds configuration for resilient service clients
type ResilientClientConfig struct {
	// Circuit breaker configuration
	CircuitBreakerName     string
	CircuitBreakerTimeout  time.Duration
	CircuitBreakerInterval time.Duration
	MaxRequests            uint32
	FailureThreshold       uint32

	// Retry configuration
	MaxRetries          int
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64

	// Observability
	Logger *slog.Logger
}

// DefaultResilientClientConfig returns a ResilientClientConfig with sensible defaults
func DefaultResilientClientConfig(name string) ResilientClientConfig {
	return ResilientClientConfig{
		CircuitBreakerName:     name,
		CircuitBreakerTimeout:  defaults.DefaultRPCTimeout,
		CircuitBreakerInterval: defaults.DefaultCircuitBreakerTimeout,
		MaxRequests:            1,
		FailureThreshold:       5,
		MaxRetries:             3,
		InitialInterval:        defaults.DefaultRetryDelay,
		MaxInterval:            defaults.DefaultMaxRetryInterval,
		Multiplier:             2.0,
		RandomizationFactor:    0.5,
		Logger:                 nil, // Will use slog.Default()
	}
}

// ResilientClient provides circuit breaker and retry capabilities for any client
type ResilientClient struct {
	circuitBreaker *CircuitBreaker
	retryConfig    RetryConfig
	logger         *slog.Logger
}

// NewResilientClient creates a new resilient client wrapper
func NewResilientClient(config ResilientClientConfig) *ResilientClient {
	cbConfig, retryConfig := applyConfigDefaults(&config)
	cb := NewCircuitBreaker(cbConfig, config.Logger)

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ResilientClient{
		circuitBreaker: cb,
		retryConfig:    retryConfig,
		logger:         logger,
	}
}

// applyConfigDefaults applies default values to ResilientClientConfig
func applyConfigDefaults(config *ResilientClientConfig) (CircuitBreakerConfig, RetryConfig) {
	// Apply logger default
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Apply circuit breaker defaults
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "default"
	}
	if config.CircuitBreakerTimeout == 0 {
		config.CircuitBreakerTimeout = defaults.DefaultRPCTimeout
	}
	if config.CircuitBreakerInterval == 0 {
		config.CircuitBreakerInterval = defaults.DefaultCircuitBreakerTimeout
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}

	// Create circuit breaker config
	cbConfig := CircuitBreakerConfig{
		Name:        config.CircuitBreakerName,
		MaxRequests: config.MaxRequests,
		Interval:    config.CircuitBreakerInterval,
		Timeout:     config.CircuitBreakerTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= config.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			config.Logger.Info("circuit breaker state changed",
				"service", name,
				"from", from.String(),
				"to", to.String())
		},
	}

	// Apply retry defaults
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.InitialInterval == 0 {
		config.InitialInterval = defaults.DefaultRetryDelay
	}
	if config.MaxInterval == 0 {
		config.MaxInterval = defaults.DefaultMaxRetryInterval
	}
	if config.Multiplier == 0 {
		config.Multiplier = 2.0
	}
	if config.RandomizationFactor == 0 {
		config.RandomizationFactor = 0.5
	}

	// Create retry config
	retryConfig := RetryConfig{
		MaxRetries:          config.MaxRetries,
		InitialInterval:     config.InitialInterval,
		MaxInterval:         config.MaxInterval,
		Multiplier:          config.Multiplier,
		RandomizationFactor: config.RandomizationFactor,
	}

	return cbConfig, retryConfig
}

// CircuitBreaker returns the underlying circuit breaker for state inspection
func (r *ResilientClient) CircuitBreaker() *CircuitBreaker {
	return r.circuitBreaker
}

// RetryConfig returns the retry configuration
func (r *ResilientClient) RetryConfig() RetryConfig {
	return r.retryConfig
}

// Logger returns the configured logger
func (r *ResilientClient) Logger() *slog.Logger {
	return r.logger
}

// ExecuteWithResilience wraps a call with circuit breaker and retry logic
// This is a generic function that can be used with any return type
func ExecuteWithResilience[T any](
	ctx context.Context,
	r *ResilientClient,
	operationName string,
	fn func() (T, error),
) (T, error) {
	return executeWithResilienceConfig(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		operationName,
		fn,
	)
}

// ExecuteWithResilienceNoRetry wraps a call with circuit breaker but no retry
// Use this for non-idempotent operations
func ExecuteWithResilienceNoRetry[T any](
	ctx context.Context,
	r *ResilientClient,
	operationName string,
	fn func() (T, error),
) (T, error) {
	return executeWithResilienceConfig(
		ctx,
		r.circuitBreaker,
		NoRetryConfig(),
		r.logger,
		operationName,
		fn,
	)
}

// executeWithResilienceConfig is the internal implementation with explicit config
func executeWithResilienceConfig[T any](
	ctx context.Context,
	cb *CircuitBreaker,
	retryConfig RetryConfig,
	logger *slog.Logger,
	operationName string,
	fn func() (T, error),
) (T, error) {
	var result T

	// Wrap the operation with retry logic
	err := Retry(ctx, retryConfig, func() error {
		// Execute through circuit breaker
		res, cbErr := cb.Execute(ctx, func() (any, error) {
			return fn()
		})
		if cbErr != nil {
			logger.Debug("operation failed",
				"operation", operationName,
				"error", cbErr)
			return fmt.Errorf("circuit breaker execution failed: %w", cbErr)
		}

		// Type assertion with check
		var ok bool
		result, ok = res.(T)
		if !ok {
			return fmt.Errorf("%w: expected %T, got %T", ErrTypeAssertion, result, res)
		}
		return nil
	})
	if err != nil {
		// Check if circuit breaker is open
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			logger.Warn("circuit breaker open",
				"operation", operationName)
		}
		return result, fmt.Errorf("resilient operation failed for %s: %w", operationName, err)
	}

	return result, nil
}
