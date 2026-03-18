package clients_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errOperationFailed = errors.New("operation failed")
	errStillFailing    = errors.New("still failing")
	successString      = "success"
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

// TestNewCircuitBreaker_NilLogger verifies default logger is used when nil provided
func TestNewCircuitBreaker_NilLogger(t *testing.T) {
	t.Parallel()

	config := clients.DefaultCircuitBreakerConfig("test-service")
	cb := clients.NewCircuitBreaker(config, nil)

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
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

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

	// Wait for timeout to transition to half-open
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

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

	// Wait for timeout to transition to half-open
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

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
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

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
		//nolint:forbidigo // simulates slow operation latency to trigger context deadline exceeded
		time.Sleep(100 * time.Millisecond)
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
				//nolint:forbidigo // simulates concurrent work latency to test thread-safety
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

	// Wait for interval to reset counts.
	//nolint:forbidigo // triggers circuit breaker interval timer reset; no observable state change to poll
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

// TestCircuitBreaker_ConcurrentOpenCircuit verifies all goroutines receive ErrOpenState when circuit is open
func TestCircuitBreaker_ConcurrentOpenCircuit(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}
	require.Equal(t, gobreaker.StateOpen, cb.State())

	// Launch multiple goroutines simultaneously hitting the open circuit
	numGoroutines := 20
	var wg sync.WaitGroup
	var openStateErrors atomic.Int32

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := cb.Execute(ctx, func() (any, error) {
				return successString, nil
			})
			if errors.Is(err, gobreaker.ErrOpenState) {
				openStateErrors.Add(1)
			}
		}()
	}
	wg.Wait()

	// All goroutines should receive ErrOpenState
	assert.Equal(t, int32(numGoroutines), openStateErrors.Load(),
		"all goroutines should receive ErrOpenState when circuit is open")
}

// TestCircuitBreaker_MaxRequestsInHalfOpen verifies MaxRequests limits concurrent requests in half-open state
func TestCircuitBreaker_MaxRequestsInHalfOpen(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	maxRequests := uint32(2)
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: maxRequests,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}
	require.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for half-open state
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

	// Track requests that actually execute
	var executedCount atomic.Int32
	var tooManyRequestsCount atomic.Int32
	var wg sync.WaitGroup
	numGoroutines := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := cb.Execute(ctx, func() (any, error) {
				executedCount.Add(1)
				//nolint:forbidigo // holds the slot to test MaxRequests limiting in half-open state
				time.Sleep(50 * time.Millisecond)
				return successString, nil
			})
			if errors.Is(err, gobreaker.ErrTooManyRequests) {
				tooManyRequestsCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// Only MaxRequests should have executed, rest should get ErrTooManyRequests
	executed := executedCount.Load()
	tooMany := tooManyRequestsCount.Load()
	assert.LessOrEqual(t, executed, int32(maxRequests),
		"should not execute more than MaxRequests in half-open state")
	assert.GreaterOrEqual(t, tooMany, int32(numGoroutines)-int32(maxRequests),
		"excess requests should get ErrTooManyRequests")
}

// TestCircuitBreaker_ReadyToTripFailureRatio verifies custom ReadyToTrip with failure ratio
func TestCircuitBreaker_ReadyToTripFailureRatio(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Trip when failure ratio exceeds 50% with at least 6 requests
			if counts.Requests < 6 {
				return false
			}
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return failureRatio > 0.5
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	t.Run("below_request_threshold", func(t *testing.T) {
		// Execute 2 failures and 2 successes (50% failure ratio, but < 6 requests)
		for i := 0; i < 2; i++ {
			_, _ = cb.Execute(ctx, func() (any, error) {
				return nil, errOperationFailed
			})
			_, _ = cb.Execute(ctx, func() (any, error) {
				return successString, nil
			})
		}

		// Should still be closed (only 4 requests)
		assert.Equal(t, gobreaker.StateClosed, cb.State(),
			"circuit should remain closed when below request threshold")
	})

	t.Run("above_threshold_with_high_failure_ratio", func(t *testing.T) {
		// Execute 2 more failures (now 6 requests, 66% failure > 50%)
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})

		// Should now be open (6 requests, 66% failure ratio > 50%)
		assert.Equal(t, gobreaker.StateOpen, cb.State(),
			"circuit should open when failure ratio exceeds threshold")
	})
}

// TestCircuitBreaker_ContextCancellationInHalfOpen verifies context cancellation during half-open state
func TestCircuitBreaker_ContextCancellationInHalfOpen(t *testing.T) {
	// Not parallel - uses time-sensitive operations

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "test-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     100 * time.Millisecond,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)

	// Trip the circuit
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return nil, errOperationFailed
		})
	}
	require.Equal(t, gobreaker.StateOpen, cb.State())

	// Wait for half-open state
	awaitErr := await.AtMost(500 * time.Millisecond).PollInterval(10 * time.Millisecond).Until(func() bool {
		return cb.State() == gobreaker.StateHalfOpen
	})
	require.NoError(t, awaitErr, "circuit should transition to half-open")

	// Create a context that will be cancelled during execution
	ctx, cancel := context.WithCancel(context.Background())

	// Execute a request that takes longer than the context cancellation
	var wg sync.WaitGroup
	wg.Add(1)
	var execErr error
	go func() {
		defer wg.Done()
		_, execErr = cb.Execute(ctx, func() (any, error) {
			//nolint:forbidigo // simulates slow operation latency to test context cancellation in half-open state
			time.Sleep(200 * time.Millisecond)
			return successString, nil
		})
	}()

	//nolint:forbidigo // ensures context is cancelled while the slow operation is still in-flight
	time.Sleep(50 * time.Millisecond)
	cancel()

	wg.Wait()

	// The execution should return a context cancellation error
	require.Error(t, execErr)
	assert.ErrorIs(t, execErr, context.Canceled,
		"should return context.Canceled when context is cancelled during half-open execution")
}

// Benchmark tests

// BenchmarkCircuitBreaker_Execute measures overhead of circuit breaker wrapper
func BenchmarkCircuitBreaker_Execute(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.DefaultCircuitBreakerConfig("bench-service")
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return successString, nil
		})
	}
}

// BenchmarkCircuitBreaker_Execute_Parallel measures circuit breaker under concurrent load
func BenchmarkCircuitBreaker_Execute_Parallel(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.DefaultCircuitBreakerConfig("bench-service")
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cb.Execute(ctx, func() (any, error) {
				return successString, nil
			})
		}
	})
}

// BenchmarkCircuitBreaker_StateCheck measures state check overhead
func BenchmarkCircuitBreaker_StateCheck(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.DefaultCircuitBreakerConfig("bench-service")
	cb := clients.NewCircuitBreaker(config, logger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.State()
	}
}

// BenchmarkCircuitBreaker_OpenState measures execute performance when circuit is open
func BenchmarkCircuitBreaker_OpenState(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := clients.CircuitBreakerConfig{
		Name:        "bench-service",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     60 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 1
		},
	}
	cb := clients.NewCircuitBreaker(config, logger)
	ctx := context.Background()

	// Trip the circuit
	_, _ = cb.Execute(ctx, func() (any, error) {
		return nil, errOperationFailed
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cb.Execute(ctx, func() (any, error) {
			return successString, nil
		})
	}
}
