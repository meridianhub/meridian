package clients

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errSomeError = errors.New("some error")

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxRetries != 3 {
		t.Errorf("expected MaxRetries = 3, got %d", config.MaxRetries)
	}
	if config.InitialInterval != 100*time.Millisecond {
		t.Errorf("expected InitialInterval = 100ms, got %v", config.InitialInterval)
	}
	if config.MaxInterval != 10*time.Second {
		t.Errorf("expected MaxInterval = 10s, got %v", config.MaxInterval)
	}
	if config.Multiplier != 2.0 {
		t.Errorf("expected Multiplier = 2.0, got %f", config.Multiplier)
	}
	if config.RandomizationFactor != 0.5 {
		t.Errorf("expected RandomizationFactor = 0.5, got %f", config.RandomizationFactor)
	}
}

func TestNoRetryConfig(t *testing.T) {
	config := NoRetryConfig()

	if config.MaxRetries != 0 {
		t.Errorf("expected MaxRetries = 0, got %d", config.MaxRetries)
	}
	if config.InitialInterval != 0 {
		t.Errorf("expected InitialInterval = 0, got %v", config.InitialInterval)
	}
	if config.MaxInterval != 0 {
		t.Errorf("expected MaxInterval = 0, got %v", config.MaxInterval)
	}
	if config.Multiplier != 1.0 {
		t.Errorf("expected Multiplier = 1.0, got %f", config.Multiplier)
	}
	if config.RandomizationFactor != 0 {
		t.Errorf("expected RandomizationFactor = 0, got %f", config.RandomizationFactor)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// Retryable errors
		{
			name:     "unavailable error",
			err:      status.Error(codes.Unavailable, "service unavailable"),
			expected: true,
		},
		{
			name:     "deadline exceeded error",
			err:      status.Error(codes.DeadlineExceeded, "deadline exceeded"),
			expected: true,
		},
		{
			name:     "resource exhausted error",
			err:      status.Error(codes.ResourceExhausted, "resource exhausted"),
			expected: true,
		},
		{
			name:     "internal error",
			err:      status.Error(codes.Internal, "internal error"),
			expected: true,
		},

		// Non-retryable errors
		{
			name:     "invalid argument error",
			err:      status.Error(codes.InvalidArgument, "invalid argument"),
			expected: false,
		},
		{
			name:     "not found error",
			err:      status.Error(codes.NotFound, "not found"),
			expected: false,
		},
		{
			name:     "already exists error",
			err:      status.Error(codes.AlreadyExists, "already exists"),
			expected: false,
		},
		{
			name:     "permission denied error",
			err:      status.Error(codes.PermissionDenied, "permission denied"),
			expected: false,
		},
		{
			name:     "unauthenticated error",
			err:      status.Error(codes.Unauthenticated, "unauthenticated"),
			expected: false,
		},
		{
			name:     "failed precondition error",
			err:      status.Error(codes.FailedPrecondition, "failed precondition"),
			expected: false,
		},
		{
			name:     "aborted error",
			err:      status.Error(codes.Aborted, "aborted"),
			expected: false,
		},
		{
			name:     "out of range error",
			err:      status.Error(codes.OutOfRange, "out of range"),
			expected: false,
		},
		{
			name:     "unimplemented error",
			err:      status.Error(codes.Unimplemented, "unimplemented"),
			expected: false,
		},
		{
			name:     "data loss error",
			err:      status.Error(codes.DataLoss, "data loss"),
			expected: false,
		},
		{
			name:     "ok status is not retryable",
			err:      status.Error(codes.OK, "ok"),
			expected: false,
		},
		{
			name:     "canceled gRPC status is not retryable",
			err:      status.Error(codes.Canceled, "canceled"),
			expected: false,
		},
		{
			name:     "unknown gRPC status is not retryable",
			err:      status.Error(codes.Unknown, "unknown"),
			expected: false,
		},

		// Edge case: undefined gRPC code hits default case
		{
			name:     "undefined gRPC code is not retryable",
			err:      status.Error(codes.Code(99), "undefined code"),
			expected: false,
		},

		// Context errors (never retry)
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: false,
		},

		// Generic errors
		{
			name:     "generic error",
			err:      errSomeError,
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestRetrySuccessOnFirstAttempt(t *testing.T) {
	config := DefaultRetryConfig()
	ctx := context.Background()

	attempts := 0
	fn := func() error {
		attempts++
		return nil
	}

	err := Retry(ctx, config, fn)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetrySuccessAfterTransientFailure(t *testing.T) {
	config := DefaultRetryConfig()
	config.InitialInterval = 10 * time.Millisecond // Speed up test
	ctx := context.Background()

	attempts := 0
	fn := func() error {
		attempts++
		if attempts < 3 {
			return status.Error(codes.Unavailable, "service unavailable")
		}
		return nil
	}

	start := time.Now()
	err := Retry(ctx, config, fn)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	// Verify exponential backoff occurred (at least 2 delays)
	// First retry: ~10ms, second retry: ~20ms (with jitter)
	// Conservative check: at least 15ms total
	if elapsed < 15*time.Millisecond {
		t.Errorf("expected delays from backoff, got %v", elapsed)
	}
}

func TestRetryMaxRetriesExhausted(t *testing.T) {
	config := DefaultRetryConfig()
	config.MaxRetries = 2
	config.InitialInterval = 10 * time.Millisecond // Speed up test
	ctx := context.Background()

	attempts := 0
	fn := func() error {
		attempts++
		return status.Error(codes.Unavailable, "service unavailable")
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("expected error after max retries, got nil")
	}
	// Max retries = 2 means initial attempt + 2 retries = 3 total attempts
	if attempts != 3 {
		t.Errorf("expected 3 attempts (initial + 2 retries), got %d", attempts)
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	config := DefaultRetryConfig()
	ctx := context.Background()

	attempts := 0
	fn := func() error {
		attempts++
		return status.Error(codes.InvalidArgument, "invalid argument")
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("expected error, got nil")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retries for non-retryable error), got %d", attempts)
	}
}

func TestRetryContextCancellation(t *testing.T) {
	config := DefaultRetryConfig()
	config.InitialInterval = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	fn := func() error {
		attempts++
		if attempts == 2 {
			cancel() // Cancel context on second attempt
		}
		return status.Error(codes.Unavailable, "service unavailable")
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
	// Should stop retrying after context is cancelled
	if attempts > 2 {
		t.Errorf("expected at most 2 attempts before cancellation, got %d", attempts)
	}
}

func TestRetryContextDeadlineExceeded(t *testing.T) {
	config := DefaultRetryConfig()
	config.InitialInterval = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	attempts := 0
	fn := func() error {
		attempts++
		//nolint:forbidigo // simulates slow operation latency to trigger context deadline
		time.Sleep(30 * time.Millisecond)
		return status.Error(codes.Unavailable, "service unavailable")
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded error, got %v", err)
	}
	// Should stop when context deadline is exceeded
	if attempts > 3 {
		t.Errorf("expected limited attempts due to context deadline, got %d", attempts)
	}
}

func TestRetryExponentialBackoff(t *testing.T) {
	// Skip timing-sensitive tests in short mode (e.g., CI environments)
	// These tests can be flaky due to CPU scheduling variance
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	config := RetryConfig{
		MaxRetries:          3,
		InitialInterval:     50 * time.Millisecond,
		MaxInterval:         1 * time.Second,
		Multiplier:          2.0,
		RandomizationFactor: 0.0, // No jitter for predictable timing
	}
	ctx := context.Background()

	attempts := 0
	attemptTimes := []time.Time{}
	fn := func() error {
		attempts++
		attemptTimes = append(attemptTimes, time.Now())
		return status.Error(codes.Unavailable, "service unavailable")
	}

	_ = Retry(ctx, config, fn)

	// Verify exponential backoff intervals
	// Attempt 1: immediate
	// Attempt 2: after ~50ms
	// Attempt 3: after ~100ms (50ms * 2)
	// Attempt 4: after ~200ms (100ms * 2)

	if len(attemptTimes) != 4 {
		t.Fatalf("expected 4 attempts, got %d", len(attemptTimes))
	}

	// Check interval between attempt 1 and 2 (should be ~50ms with no jitter)
	// Increased tolerance to ±30% to account for CI runner variance
	interval1 := attemptTimes[1].Sub(attemptTimes[0])
	if interval1 < 35*time.Millisecond || interval1 > 65*time.Millisecond {
		t.Errorf("expected first retry interval ~50ms (±30%%), got %v", interval1)
	}

	// Check interval between attempt 2 and 3 (should be ~100ms)
	// Increased tolerance to ±30% to account for CI runner variance
	interval2 := attemptTimes[2].Sub(attemptTimes[1])
	if interval2 < 70*time.Millisecond || interval2 > 130*time.Millisecond {
		t.Errorf("expected second retry interval ~100ms (±30%%), got %v", interval2)
	}

	// Check interval between attempt 3 and 4 (should be ~200ms)
	// Increased tolerance to ±30% to account for CI runner variance
	interval3 := attemptTimes[3].Sub(attemptTimes[2])
	if interval3 < 140*time.Millisecond || interval3 > 260*time.Millisecond {
		t.Errorf("expected third retry interval ~200ms (±30%%), got %v", interval3)
	}
}

func TestRetryJitterIsApplied(t *testing.T) {
	// Skip timing-sensitive tests in short mode (e.g., CI environments)
	if testing.Short() {
		t.Skip("Skipping timing-sensitive test in short mode")
	}

	config := RetryConfig{
		MaxRetries:          2,
		InitialInterval:     100 * time.Millisecond,
		MaxInterval:         1 * time.Second,
		Multiplier:          2.0,
		RandomizationFactor: 0.5, // ±50% jitter
	}
	ctx := context.Background()

	// Run multiple times to verify jitter varies
	intervals := []time.Duration{}
	for i := 0; i < 5; i++ {
		attempts := 0
		var firstAttempt, secondAttempt time.Time
		fn := func() error {
			attempts++
			switch attempts {
			case 1:
				firstAttempt = time.Now()
			case 2:
				secondAttempt = time.Now()
			}
			return status.Error(codes.Unavailable, "service unavailable")
		}

		_ = Retry(ctx, config, fn)
		if !firstAttempt.IsZero() && !secondAttempt.IsZero() {
			intervals = append(intervals, secondAttempt.Sub(firstAttempt))
		}
	}

	// Verify that intervals vary (jitter is working)
	// With ±50% jitter, intervals should be between 50ms and 150ms
	allSame := true
	for i := 1; i < len(intervals); i++ {
		if intervals[i] != intervals[0] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Error("expected intervals to vary due to jitter, but all were the same")
	}

	// Verify all intervals are within expected range
	// Wider tolerance (±60% vs ±30% in exponential backoff test) because:
	// 1. Test uses 50% randomization factor (±50% jitter by design)
	// 2. CI scheduler variance compounds with intentional randomization
	// Expected: 100ms ± 50% jitter ± CI variance = 40-160ms range
	for _, interval := range intervals {
		if interval < 40*time.Millisecond || interval > 160*time.Millisecond {
			t.Errorf("expected interval between 40ms and 160ms with jitter, got %v", interval)
		}
	}
}

func TestRetryWithContextError(t *testing.T) {
	config := DefaultRetryConfig()
	ctx := context.Background()

	attempts := 0
	fn := func() error {
		attempts++
		return context.Canceled
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retries for context errors), got %d", attempts)
	}
}

func TestRetryWithCircuitBreakerIntegration(t *testing.T) {
	// This test demonstrates the recommended pattern: Circuit Breaker wraps Retry
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cbConfig := DefaultCircuitBreakerConfig("test-service")
	cbConfig.Timeout = 1 * time.Second
	cb := NewCircuitBreaker(cbConfig, logger)

	retryConfig := DefaultRetryConfig()
	retryConfig.MaxRetries = 2
	retryConfig.InitialInterval = 10 * time.Millisecond

	ctx := context.Background()

	// Simulate a service that fails twice then succeeds
	attempts := 0
	_, err := cb.Execute(ctx, func() (any, error) {
		return nil, Retry(ctx, retryConfig, func() error {
			attempts++
			if attempts < 3 {
				return status.Error(codes.Unavailable, "service unavailable")
			}
			return nil
		})
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if cb.State() != 0 { // 0 = Closed state
		t.Errorf("expected circuit breaker to be closed, got state %v", cb.State())
	}
}
