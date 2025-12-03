package clients_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errOperationFailed = errors.New("operation failed")
	errStillFailing    = errors.New("still failing")
)

// TestNewCircuitBreaker_WithDefaultConfig verifies circuit breaker is created with default configuration
func TestNewCircuitBreaker_WithDefaultConfig(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")

	cb := clients.NewCircuitBreaker(config, logger)

	assert.NotNil(t, cb)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestNewCircuitBreaker_WithCustomConfig verifies circuit breaker respects custom configuration
func TestNewCircuitBreaker_WithCustomConfig(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "custom-service",
		MaxRequests: 5,
		Interval:    30 * time.Second,
		Timeout:     15 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
		OnStateChange: func(_ string, _ gobreaker.State, _ gobreaker.State) {
			// Custom state change handler
		},
	}

	cb := clients.NewCircuitBreaker(config, logger)

	assert.NotNil(t, cb)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestCircuitBreaker_Execute_Success verifies successful execution returns result
func TestCircuitBreaker_Execute_Success(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	result, err := cb.Execute(ctx, func() (any, error) {
		return successString, nil
	})

	require.NoError(t, err)
	assert.Equal(t, successString, result)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestCircuitBreaker_Execute_Failure verifies failure is propagated
func TestCircuitBreaker_Execute_Failure(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	result, err := cb.Execute(ctx, func() (any, error) {
		return nil, errOperationFailed
	})

	require.Error(t, err)
	assert.Equal(t, errOperationFailed, err)
	assert.Nil(t, result)
	assert.Equal(t, gobreaker.StateClosed, cb.State(), "should remain closed after single failure")
}

// TestCircuitBreaker_OpensAfterThresholdFailures verifies circuit opens after consecutive failures
func TestCircuitBreaker_OpensAfterThresholdFailures(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute 5 consecutive failures (default threshold)
	for i := 0; i < 5; i++ {
		_, err := cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
		require.Error(t, err)
	}

	// Circuit should now be open
	assert.Equal(t, gobreaker.StateOpen, cb.State())

	// Next request should fail immediately with circuit breaker error
	_, err := cb.Execute(ctx, func() (any, error) {
		t.Fatal("should not execute function when circuit is open")
		return nil, nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, gobreaker.ErrOpenState)
}

// TestCircuitBreaker_TransitionsToHalfOpen verifies circuit transitions to half-open after timeout
func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond, // Short timeout for test
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Trigger circuit to open (3 consecutive failures)
	for i := 0; i < 3; i++ {
		_, err := cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
		require.Error(t, err)
	}

	assert.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for timeout to transition to half-open
	time.Sleep(150 * time.Millisecond)

	// Next request should transition to half-open and execute
	_, err := cb.Execute(ctx, func() (any, error) {
		return successString, nil
	})

	require.NoError(t, err)
	// After successful request in half-open, circuit should close
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestCircuitBreaker_ClosesAfterSuccessInHalfOpen verifies circuit closes after successful requests in half-open
func TestCircuitBreaker_ClosesAfterSuccessInHalfOpen(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 2, // Allow 2 requests in half-open
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Trigger circuit to open
	for i := 0; i < 3; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}

	assert.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Execute successful requests in half-open
	for i := 0; i < 2; i++ {
		_, err := cb.Execute(ctx, func() (any, error) {
			return successString, nil
		})
		require.NoError(t, err)
	}

	// Circuit should be closed after consecutive successes
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestCircuitBreaker_ReopensOnFailureInHalfOpen verifies circuit reopens if request fails in half-open
func TestCircuitBreaker_ReopensOnFailureInHalfOpen(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Trigger circuit to open
	for i := 0; i < 3; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}

	assert.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Execute failing request in half-open
	_, err := cb.Execute(ctx, func() (any, error) {
		return nil, errStillFailing
	})

	require.Error(t, err)
	// Circuit should reopen after failure in half-open
	assert.Equal(t, gobreaker.StateOpen, cb.State())
}

// TestCircuitBreaker_StateChangeCallback verifies state change callback is invoked
func TestCircuitBreaker_StateChangeCallback(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	stateChanges := make([]string, 0)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
		OnStateChange: func(_ string, from gobreaker.State, to gobreaker.State) {
			stateChanges = append(stateChanges, from.String()+"->"+to.String())
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Trigger state transitions: closed -> open -> half-open -> closed
	// Closed -> Open
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}

	// Open -> Half-Open
	time.Sleep(150 * time.Millisecond)

	// Half-Open -> Closed
	_, _ = cb.Execute(ctx, func() (any, error) {
		return successString, nil
	})

	// Verify state changes were recorded
	require.GreaterOrEqual(t, len(stateChanges), 2, "should have at least closed->open and half-open->closed transitions")
	assert.Contains(t, stateChanges[0], "closed")
	assert.Contains(t, stateChanges[0], "open")
}

// TestCircuitBreaker_ContextCancellation verifies context cancellation is respected
func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := cb.Execute(ctx, func() (any, error) {
		return "should not execute", nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestCircuitBreaker_ContextTimeout verifies context timeout is respected
func TestCircuitBreaker_ContextTimeout(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := cb.Execute(ctx, func() (any, error) {
		time.Sleep(100 * time.Millisecond) // Longer than timeout
		return "should timeout", nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestDefaultCircuitBreakerConfig verifies default configuration values
func TestDefaultCircuitBreakerConfig(t *testing.T) {
	t.Parallel()

	config := clients.DefaultCircuitBreakerConfig("test-service")

	assert.Equal(t, "test-service", config.Name)
	assert.Equal(t, uint32(1), config.MaxRequests)
	assert.Equal(t, 60*time.Second, config.Interval)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.NotNil(t, config.ReadyToTrip)
	assert.Nil(t, config.OnStateChange)

	// Test default ReadyToTrip function
	assert.False(t, config.ReadyToTrip(gobreaker.Counts{ConsecutiveFailures: 4}))
	assert.True(t, config.ReadyToTrip(gobreaker.Counts{ConsecutiveFailures: 5}))
}

// TestCircuitBreaker_ConcurrentExecution verifies circuit breaker works with concurrent requests
func TestCircuitBreaker_ConcurrentExecution(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()
	numGoroutines := 10
	done := make(chan bool, numGoroutines)

	// Execute concurrent successful requests
	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := cb.Execute(ctx, func() (any, error) {
				time.Sleep(10 * time.Millisecond)
				return successString, nil
			})
			done <- err == nil
		}()
	}

	// Wait for all goroutines to complete
	successCount := 0
	for i := 0; i < numGoroutines; i++ {
		if <-done {
			successCount++
		}
	}

	// All should succeed when circuit is closed
	assert.Equal(t, numGoroutines, successCount)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

// TestCircuitBreaker_ResetAfterInterval verifies circuit resets counts after interval in closed state
func TestCircuitBreaker_ResetAfterInterval(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    200 * time.Millisecond, // Short interval for test
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	ctx := context.Background()

	// Execute 3 failures (below threshold)
	for i := 0; i < 3; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}

	assert.Equal(t, gobreaker.StateClosed, cb.State(), "should remain closed below threshold")

	// Wait for interval to reset counts
	time.Sleep(250 * time.Millisecond)

	// Execute 3 more failures (should not trip because counts were reset)
	for i := 0; i < 3; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}

	// Circuit should still be closed because counts were reset after interval
	assert.Equal(t, gobreaker.StateClosed, cb.State(), "should remain closed after interval reset")
}
