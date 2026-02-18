package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"strings"
	"syscall"
	"time"
)

// PermanentError wraps an error to signal that it should not be retried.
// Use Permanent() to create one.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	if e.Err == nil {
		return "permanent: <nil>"
	}
	return "permanent: " + e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// Permanent wraps err to indicate that it is a permanent (non-retryable) error.
// Returns nil if err is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err (or any error in its chain) is a PermanentError.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// transientSubstrings are substrings in error messages that indicate
// transient infrastructure errors worth retrying during startup.
var transientSubstrings = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"dial tcp",
	"server is not ready",
	"node is not ready",
}

// IsRetryableStartupError classifies err as retryable (transient infrastructure
// error) or not. It returns false for nil, PermanentError, and unrecognized errors.
func IsRetryableStartupError(err error) bool {
	if err == nil {
		return false
	}
	if IsPermanent(err) {
		return false
	}

	// Check for net.OpError in the chain.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check for known transient syscall errors in the chain.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	// Fall back to substring matching on the error message.
	msg := strings.ToLower(err.Error())
	for _, sub := range transientSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}

	return false
}

// retryConfig holds configuration for RunWithRetry.
type retryConfig struct {
	maxAttempts int
	initialWait time.Duration
	maxWait     time.Duration
	multiplier  float64
	logger      *slog.Logger
}

// RetryOption configures RunWithRetry behavior.
type RetryOption func(*retryConfig)

// WithMaxAttempts sets the maximum number of attempts (default: 10).
func WithMaxAttempts(n int) RetryOption {
	return func(c *retryConfig) { c.maxAttempts = n }
}

// WithInitialWait sets the initial wait duration between retries (default: 1s).
func WithInitialWait(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.initialWait = d }
}

// WithMaxWait sets the maximum wait duration between retries (default: 30s).
func WithMaxWait(d time.Duration) RetryOption {
	return func(c *retryConfig) { c.maxWait = d }
}

// WithRetryLogger sets a structured logger for retry events.
func WithRetryLogger(l *slog.Logger) RetryOption {
	return func(c *retryConfig) { c.logger = l }
}

// RunWithRetry executes fn with exponential backoff retry for transient startup
// errors. Permanent errors (wrapped with Permanent) cause immediate return with
// no further retries. Unknown errors are retried conservatively.
func RunWithRetry(fn func() error, opts ...RetryOption) error {
	cfg := retryConfig{
		maxAttempts: 10,
		initialWait: 1 * time.Second,
		maxWait:     30 * time.Second,
		multiplier:  2.0,
	}
	for _, o := range opts {
		o(&cfg)
	}

	wait := cfg.initialWait
	var lastErr error

	for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if IsPermanent(lastErr) {
			if cfg.logger != nil {
				cfg.logger.Error("permanent startup error, not retrying",
					"attempt", attempt,
					"error", lastErr,
				)
			}
			return lastErr
		}

		if attempt == cfg.maxAttempts {
			break
		}

		// Add jitter: +-20%
		jittered := applyJitter(wait)

		if cfg.logger != nil {
			cfg.logger.Warn("transient startup error, retrying",
				"attempt", attempt,
				"max_attempts", cfg.maxAttempts,
				"next_wait", jittered,
				"error", lastErr,
			)
		}

		time.Sleep(jittered)

		// Exponential increase for next iteration, capped at maxWait
		wait = time.Duration(float64(wait) * cfg.multiplier)
		if wait > cfg.maxWait {
			wait = cfg.maxWait
		}
	}

	if cfg.logger != nil {
		cfg.logger.Error("all startup retry attempts exhausted",
			"attempts", cfg.maxAttempts,
			"error", lastErr,
		)
	}

	return fmt.Errorf("startup failed after %d attempts: %w", cfg.maxAttempts, lastErr)
}

// applyJitter adds +-20% jitter to a duration to avoid thundering herd.
func applyJitter(d time.Duration) time.Duration {
	// jitter in range [-0.2, +0.2]
	jitter := (rand.Float64()*0.4 - 0.2)
	return time.Duration(float64(d) * (1.0 + jitter))
}
