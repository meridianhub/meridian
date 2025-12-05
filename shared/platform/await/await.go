// Package await provides utilities for polling conditions in tests.
// It eliminates the need for time.Sleep by repeatedly checking a condition
// until it becomes true or a timeout is reached.
//
// Inspired by Java's Awaitility library, this package provides a fluent API
// for expressing test synchronization intent clearly.
//
// Note: This is a lightweight, zero-dependency implementation. If more advanced
// features are needed (matchers, async assertions, etc.), consider migrating to
// gomega.Eventually() and deleting this package. See: github.com/onsi/gomega
//
// Example usage:
//
//	// Wait for a condition with defaults (10s timeout, 100ms poll interval)
//	err := await.Until(func() bool {
//	    return repo.FindByID(ctx, id) != nil
//	})
//
//	// With custom timeout and poll interval
//	err := await.New().
//	    AtMost(5 * time.Second).
//	    PollInterval(50 * time.Millisecond).
//	    Until(func() bool {
//	        return order.Status == "COMPLETED"
//	    })
//
//	// With context support
//	err := await.New().
//	    WithContext(ctx).
//	    AtMost(10 * time.Second).
//	    Until(func() bool {
//	        return condition()
//	    })
package await

import (
	"context"
	"errors"
	"time"
)

// Default configuration values.
const (
	DefaultTimeout      = 10 * time.Second
	DefaultPollInterval = 100 * time.Millisecond
)

// ErrTimeout is returned when the condition is not met within the timeout period.
var ErrTimeout = errors.New("await: condition not met within timeout")

// ErrContextCancelled is returned when the context is cancelled before the condition is met.
var ErrContextCancelled = errors.New("await: context cancelled")

// Awaiter provides a fluent interface for waiting on conditions.
type Awaiter struct {
	timeout      time.Duration
	pollInterval time.Duration
	ctx          context.Context
}

// New creates a new Awaiter with default configuration.
// Default timeout is 10 seconds, default poll interval is 100ms.
func New() *Awaiter {
	return &Awaiter{
		timeout:      DefaultTimeout,
		pollInterval: DefaultPollInterval,
		ctx:          context.Background(),
	}
}

// AtMost sets the maximum time to wait for the condition.
func (a *Awaiter) AtMost(timeout time.Duration) *Awaiter {
	a.timeout = timeout
	return a
}

// PollInterval sets the interval between condition checks.
func (a *Awaiter) PollInterval(interval time.Duration) *Awaiter {
	a.pollInterval = interval
	return a
}

// WithContext sets a context for cancellation support.
// If the context is cancelled, Until returns ErrContextCancelled.
func (a *Awaiter) WithContext(ctx context.Context) *Awaiter {
	a.ctx = ctx
	return a
}

// Until repeatedly calls the condition function until it returns true
// or the timeout is reached. Returns nil if the condition is met,
// ErrTimeout if the timeout expires, or ErrContextCancelled if the
// context is cancelled.
func (a *Awaiter) Until(condition func() bool) error {
	deadline := time.Now().Add(a.timeout)
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// Check immediately before waiting
	if condition() {
		return nil
	}

	for {
		select {
		case <-a.ctx.Done():
			return ErrContextCancelled
		case <-ticker.C:
			if condition() {
				return nil
			}
			if time.Now().After(deadline) {
				return ErrTimeout
			}
		}
	}
}

// UntilNoError repeatedly calls the function until it returns nil error
// or the timeout is reached. This is useful for waiting on operations
// that may fail transiently.
func (a *Awaiter) UntilNoError(fn func() error) error {
	var lastErr error
	err := a.Until(func() bool {
		lastErr = fn()
		return lastErr == nil
	})
	if errors.Is(err, ErrTimeout) && lastErr != nil {
		return lastErr
	}
	return err
}

// UntilValue repeatedly calls the function until it returns a non-nil value
// or the timeout is reached. Returns the value and nil error on success,
// or nil and the timeout error on failure.
func UntilValue[T any](a *Awaiter, fn func() *T) (*T, error) {
	var result *T
	err := a.Until(func() bool {
		result = fn()
		return result != nil
	})
	return result, err
}

// Until is a convenience function that waits for a condition with default settings.
// Equivalent to await.New().Until(condition).
func Until(condition func() bool) error {
	return New().Until(condition)
}

// UntilNoError is a convenience function that waits for a function to succeed.
// Equivalent to await.New().UntilNoError(fn).
func UntilNoError(fn func() error) error {
	return New().UntilNoError(fn)
}

// AtMost is a convenience function to start building an Awaiter with a timeout.
// Equivalent to await.New().AtMost(timeout).
func AtMost(timeout time.Duration) *Awaiter {
	return New().AtMost(timeout)
}

// PollEvery is a convenience function to start building an Awaiter with poll interval.
// Equivalent to await.New().PollInterval(interval).
func PollEvery(interval time.Duration) *Awaiter {
	return New().PollInterval(interval)
}
