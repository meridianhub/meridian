package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryConfig holds retry configuration
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (excluding the initial attempt)
	MaxRetries int

	// InitialInterval is the initial delay before the first retry
	InitialInterval time.Duration

	// MaxInterval is the maximum delay between retries
	MaxInterval time.Duration

	// Multiplier is the factor by which the interval increases after each retry
	Multiplier float64

	// RandomizationFactor adds jitter to prevent thundering herd (0.0 = no jitter, 1.0 = max jitter)
	RandomizationFactor float64
}

// DefaultRetryConfig returns a retry configuration with sensible defaults
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:          3,
		InitialInterval:     defaults.DefaultRetryDelay,
		MaxInterval:         2 * defaults.DefaultMaxRetryInterval, // 10s max interval
		Multiplier:          2.0,
		RandomizationFactor: 0.5, // ±50% jitter
	}
}

// NoRetryConfig returns a configuration that disables retries
// Use this for non-idempotent operations
func NoRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:          0,
		InitialInterval:     0,
		MaxInterval:         0,
		Multiplier:          1.0,
		RandomizationFactor: 0,
	}
}

// IsRetryable determines if an error should be retried
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Never retry context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check if it's a gRPC status error
	st, ok := status.FromError(err)
	if !ok {
		// Generic errors are not retryable by default
		return false
	}

	// Classify gRPC codes
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Internal:
		// Transient errors that may succeed on retry
		return true
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied,
		codes.Unauthenticated, codes.FailedPrecondition, codes.Aborted, codes.OutOfRange,
		codes.Unimplemented, codes.DataLoss:
		// Permanent errors that won't succeed on retry
		return false
	case codes.OK, codes.Canceled, codes.Unknown:
		// OK means no error, Canceled is handled above, Unknown is not retried
		return false
	default:
		// Unknown codes are not retried by default
		return false
	}
}

// Retry wraps a function with retry logic using exponential backoff with jitter
// The function is retried if it returns a retryable error (see IsRetryable)
// Retry respects context cancellation and deadlines
func Retry(ctx context.Context, config RetryConfig, fn func() error) error {
	// Configure exponential backoff
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = config.InitialInterval
	b.MaxInterval = config.MaxInterval
	b.Multiplier = config.Multiplier
	b.RandomizationFactor = config.RandomizationFactor
	b.MaxElapsedTime = 0 // No max elapsed time, we use MaxRetries instead
	b.Reset()

	// Wrap with context
	backoffWithContext := backoff.WithContext(b, ctx)

	// Track number of attempts
	attempt := 0
	maxAttempts := config.MaxRetries + 1 // Initial attempt + retries

	operation := func() error {
		// Check context before each attempt
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		attempt++

		// Execute the function
		err := fn()

		// If no error, we're done
		if err == nil {
			return nil
		}

		// If we've exhausted retries, return permanent error
		if attempt >= maxAttempts {
			return backoff.Permanent(err)
		}

		// If error is not retryable, return permanent error
		if !IsRetryable(err) {
			return backoff.Permanent(err)
		}

		// Return the error to trigger retry
		return err
	}

	if err := backoff.Retry(operation, backoffWithContext); err != nil {
		return fmt.Errorf("retry failed: %w", err)
	}
	return nil
}
