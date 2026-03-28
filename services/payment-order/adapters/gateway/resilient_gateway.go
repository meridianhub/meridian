// Package gateway provides resilient wrappers for PaymentGateway implementations.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/sony/gobreaker/v2"
	"golang.org/x/time/rate"
)

// Sentinel errors for resilience patterns
var (
	// ErrCircuitOpen is returned when the circuit breaker is in open state.
	ErrCircuitOpen = errors.New("circuit breaker is open")
	// ErrRateLimited is returned when the rate limit is exceeded.
	ErrRateLimited = errors.New("rate limit exceeded")
)

// ResilientGatewayConfig holds configuration for the resilient payment gateway wrapper.
type ResilientGatewayConfig struct {
	// Circuit breaker settings
	CircuitBreakerName     string
	CircuitBreakerTimeout  time.Duration // Time to wait before transitioning from open to half-open
	CircuitBreakerInterval time.Duration // Interval to clear counts in closed state
	MaxRequests            uint32        // Max requests in half-open state
	FailureThreshold       uint32        // Consecutive failures to trip the circuit

	// Rate limiting settings
	RateLimit      float64 // Requests per second
	RateLimitBurst int     // Maximum burst size

	// Retry settings
	MaxRetries          int
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64

	// Observability
	Logger *slog.Logger
}

// DefaultResilientGatewayConfig returns sensible defaults for production use.
func DefaultResilientGatewayConfig() ResilientGatewayConfig {
	return ResilientGatewayConfig{
		// Circuit breaker: trip after 5 failures, stay open for 30s
		CircuitBreakerName:     "payment-gateway",
		CircuitBreakerTimeout:  defaults.DefaultCircuitBreakerOpenTimeout,
		CircuitBreakerInterval: defaults.DefaultCircuitBreakerInterval,
		MaxRequests:            1,
		FailureThreshold:       5,

		// Rate limiting: 100 requests/sec with burst of 10
		RateLimit:      100.0,
		RateLimitBurst: 10,

		// Retry: 3 retries with exponential backoff
		MaxRetries:          3,
		InitialInterval:     defaults.DefaultRetryDelay,
		MaxInterval:         defaults.DefaultMaxRetryInterval,
		Multiplier:          2.0,
		RandomizationFactor: 0.5,

		Logger: slog.Default(),
	}
}

// ResilientPaymentGateway wraps a PaymentGateway with circuit breaker, rate limiting, and retry logic.
type ResilientPaymentGateway struct {
	delegate       PaymentGateway
	circuitBreaker *gobreaker.CircuitBreaker[PaymentResponse]
	rateLimiter    *rate.Limiter
	retryConfig    retryConfig
	logger         *slog.Logger
}

// retryConfig holds internal retry configuration.
type retryConfig struct {
	maxRetries          int
	initialInterval     time.Duration
	maxInterval         time.Duration
	multiplier          float64
	randomizationFactor float64
}

// NewResilientPaymentGateway creates a resilient wrapper around a PaymentGateway.
func NewResilientPaymentGateway(delegate PaymentGateway, config ResilientGatewayConfig) *ResilientPaymentGateway {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Configure circuit breaker
	cbSettings := gobreaker.Settings{
		Name:        config.CircuitBreakerName,
		MaxRequests: config.MaxRequests,
		Interval:    config.CircuitBreakerInterval,
		Timeout:     config.CircuitBreakerTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= config.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			config.Logger.Info("payment gateway circuit breaker state changed",
				"name", name,
				"from", from.String(),
				"to", to.String(),
			)
		},
	}

	return &ResilientPaymentGateway{
		delegate:       delegate,
		circuitBreaker: gobreaker.NewCircuitBreaker[PaymentResponse](cbSettings),
		rateLimiter:    rate.NewLimiter(rate.Limit(config.RateLimit), config.RateLimitBurst),
		retryConfig: retryConfig{
			maxRetries:          config.MaxRetries,
			initialInterval:     config.InitialInterval,
			maxInterval:         config.MaxInterval,
			multiplier:          config.Multiplier,
			randomizationFactor: config.RandomizationFactor,
		},
		logger: config.Logger,
	}
}

// SendPayment sends a payment request with circuit breaker, rate limiting, and retry protection.
func (r *ResilientPaymentGateway) SendPayment(ctx context.Context, req PaymentRequest) (PaymentResponse, error) {
	if !r.rateLimiter.Allow() {
		r.logger.Warn("payment gateway rate limit exceeded",
			"payment_order_id", req.PaymentOrderID.String(),
		)
		return PaymentResponse{}, ErrRateLimited
	}

	var result PaymentResponse

	b := r.buildBackoff()
	backoffWithContext := backoff.WithContext(b, ctx)

	attempt := 0
	maxAttempts := r.retryConfig.maxRetries + 1

	operation := func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		attempt++

		resp, err := r.circuitBreaker.Execute(func() (PaymentResponse, error) {
			return r.delegate.SendPayment(ctx, req)
		})
		if err != nil {
			return r.classifyRetryError(err, req, attempt, maxAttempts)
		}

		result = resp
		return nil
	}

	if err := backoff.Retry(operation, backoffWithContext); err != nil {
		return PaymentResponse{}, fmt.Errorf("payment gateway call failed: %w", err)
	}

	return result, nil
}

// buildBackoff creates a configured exponential backoff instance.
func (r *ResilientPaymentGateway) buildBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = r.retryConfig.initialInterval
	b.MaxInterval = r.retryConfig.maxInterval
	b.Multiplier = r.retryConfig.multiplier
	b.RandomizationFactor = r.retryConfig.randomizationFactor
	b.MaxElapsedTime = 0
	b.Reset()
	return b
}

// classifyRetryError determines whether to retry, permanently fail, or circuit-break on an error.
func (r *ResilientPaymentGateway) classifyRetryError(err error, req PaymentRequest, attempt, maxAttempts int) error {
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		r.logger.Warn("payment gateway circuit breaker open",
			"payment_order_id", req.PaymentOrderID.String(),
			"attempt", attempt,
		)
		return backoff.Permanent(fmt.Errorf("%w: %v", ErrCircuitOpen, err)) //nolint:errorlint // second error is context-only to preserve errors.Is() for sentinel
	}

	if attempt >= maxAttempts {
		r.logger.Error("payment gateway call failed after max retries",
			"payment_order_id", req.PaymentOrderID.String(),
			"attempts", attempt,
			"error", err,
		)
		return backoff.Permanent(err)
	}

	if !r.isRetryable(err) {
		return backoff.Permanent(err)
	}

	r.logger.Debug("payment gateway call failed, retrying",
		"payment_order_id", req.PaymentOrderID.String(),
		"attempt", attempt,
		"error", err,
	)
	return err
}

// isRetryable determines if an error should be retried.
// Context errors, permanent business errors, and known non-transient errors are not retried.
func (r *ResilientPaymentGateway) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Never retry context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Never retry gateway timeout errors (already timed out, retrying won't help immediately)
	if errors.Is(err, ErrGatewayTimeout) {
		return false
	}

	// Check for common permanent error patterns in the error string
	// These indicate configuration or validation issues, not transient failures
	errStr := err.Error()
	permanentPatterns := []string{
		"invalid",
		"unauthorized",
		"forbidden",
		"not found",
		"bad request",
		"validation",
	}
	for _, pattern := range permanentPatterns {
		if containsCaseInsensitive(errStr, pattern) {
			return false
		}
	}

	// Retry transient errors (network issues, connection resets, temporary failures)
	// Business logic errors from the gateway (StatusRejected) are wrapped differently
	// and handled by the saga, so they won't reach here
	return true
}

// containsCaseInsensitive checks if s contains substr (case-insensitive).
func containsCaseInsensitive(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// CircuitBreakerState returns the current state of the circuit breaker.
func (r *ResilientPaymentGateway) CircuitBreakerState() gobreaker.State {
	return r.circuitBreaker.State()
}
