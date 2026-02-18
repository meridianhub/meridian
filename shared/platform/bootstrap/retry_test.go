package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var errSentinel = errors.New("sentinel error")

func TestPermanent_NilReturnsNil(t *testing.T) {
	if got := Permanent(nil); got != nil {
		t.Errorf("Permanent(nil) = %v, want nil", got)
	}
}

func TestIsPermanent_ReturnsTrueForPermanentError(t *testing.T) {
	err := Permanent(errors.New("bad config"))
	if !IsPermanent(err) {
		t.Error("IsPermanent(Permanent(err)) = false, want true")
	}
}

func TestIsPermanent_ReturnsFalseForRegularError(t *testing.T) {
	err := fmt.Errorf("some error")
	if IsPermanent(err) {
		t.Error("IsPermanent(regular error) = true, want false")
	}
}

func TestPermanent_UnwrapChainPreservesSentinel(t *testing.T) {
	wrapped := Permanent(errSentinel)
	if !errors.Is(wrapped, errSentinel) {
		t.Error("errors.Is(Permanent(sentinel), sentinel) = false, want true")
	}
}

func TestPermanentError_ErrorMessage(t *testing.T) {
	inner := errors.New("bad config value")
	pe := Permanent(inner)
	want := "permanent: bad config value"
	if got := pe.Error(); got != want {
		t.Errorf("PermanentError.Error() = %q, want %q", got, want)
	}
}

func TestIsRetryableStartupError_Nil(t *testing.T) {
	if IsRetryableStartupError(nil) {
		t.Error("IsRetryableStartupError(nil) = true, want false")
	}
}

func TestIsRetryableStartupError_PermanentError(t *testing.T) {
	err := Permanent(errors.New("bad config"))
	if IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(PermanentError) = true, want false")
	}
}

func TestIsRetryableStartupError_NetOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Addr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 5432,
		},
		Err: syscall.ECONNREFUSED,
	}
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(net.OpError) = false, want true")
	}
}

func TestIsRetryableStartupError_WrappedNetOpError(t *testing.T) {
	inner := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Addr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 5432,
		},
		Err: syscall.ECONNREFUSED,
	}
	err := fmt.Errorf("failed to connect: %w", inner)
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(wrapped net.OpError) = false, want true")
	}
}

func TestIsRetryableStartupError_ConnectionRefusedString(t *testing.T) {
	err := fmt.Errorf("connection refused")
	if !IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError('connection refused') = false, want true")
	}
}

func TestIsRetryableStartupError_TransientStrings(t *testing.T) {
	transientMessages := []string{
		"connection refused",
		"connection reset by peer",
		"i/o timeout",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"server is not ready",
		"node is not ready",
	}

	for _, msg := range transientMessages {
		t.Run(msg, func(t *testing.T) {
			err := fmt.Errorf("startup failed: %s", msg)
			if !IsRetryableStartupError(err) {
				t.Errorf("IsRetryableStartupError(%q) = false, want true", msg)
			}
		})
	}
}

func TestIsRetryableStartupError_UnrecognizedError(t *testing.T) {
	err := errors.New("invalid configuration value")
	if IsRetryableStartupError(err) {
		t.Error("IsRetryableStartupError(unrecognized error) = true, want false")
	}
}

func TestIsRetryableStartupError_SyscallErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ECONNREFUSED", syscall.ECONNREFUSED},
		{"ETIMEDOUT", syscall.ETIMEDOUT},
		{"ECONNRESET", syscall.ECONNRESET},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("dial failed: %w", tt.err)
			if !IsRetryableStartupError(wrapped) {
				t.Errorf("IsRetryableStartupError(%s) = false, want true", tt.name)
			}
		})
	}
}

// --- RunWithRetry tests ---

func TestRunWithRetry_SucceedsOnFirstTry(t *testing.T) {
	var calls atomic.Int32
	err := RunWithRetry(func() error {
		calls.Add(1)
		return nil
	})
	if err != nil {
		t.Errorf("RunWithRetry() = %v, want nil", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fn called %d times, want 1", got)
	}
}

func TestRunWithRetry_SucceedsAfterTransientErrors(t *testing.T) {
	var calls atomic.Int32
	transient := fmt.Errorf("startup failed: connection refused")

	err := RunWithRetry(func() error {
		n := calls.Add(1)
		if n < 3 {
			return transient
		}
		return nil
	}, WithInitialWait(1*time.Millisecond), WithMaxWait(2*time.Millisecond))
	if err != nil {
		t.Errorf("RunWithRetry() = %v, want nil", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("fn called %d times, want 3", got)
	}
}

func TestRunWithRetry_PermanentErrorOnFirstAttempt(t *testing.T) {
	inner := errors.New("bad config")
	permErr := Permanent(inner)

	var calls atomic.Int32
	err := RunWithRetry(func() error {
		calls.Add(1)
		return permErr
	}, WithInitialWait(1*time.Millisecond))

	if err == nil {
		t.Fatal("RunWithRetry() = nil, want error")
	}
	if !IsPermanent(err) {
		t.Errorf("returned error is not PermanentError: %v", err)
	}
	if !errors.Is(err, inner) {
		t.Errorf("returned error does not wrap inner: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fn called %d times, want 1", got)
	}
}

func TestRunWithRetry_PermanentErrorOnSecondAttempt(t *testing.T) {
	transient := fmt.Errorf("startup failed: connection refused")
	inner := errors.New("missing required table")
	permErr := Permanent(inner)

	var calls atomic.Int32
	err := RunWithRetry(func() error {
		n := calls.Add(1)
		if n == 1 {
			return transient
		}
		return permErr
	}, WithInitialWait(1*time.Millisecond))

	if err == nil {
		t.Fatal("RunWithRetry() = nil, want error")
	}
	if !IsPermanent(err) {
		t.Errorf("returned error is not PermanentError: %v", err)
	}
	if !errors.Is(err, inner) {
		t.Errorf("returned error does not wrap inner: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("fn called %d times, want 2", got)
	}
}

func TestRunWithRetry_AllAttemptsExhausted(t *testing.T) {
	transient := fmt.Errorf("startup failed: connection refused")

	var calls atomic.Int32
	err := RunWithRetry(func() error {
		calls.Add(1)
		return transient
	}, WithMaxAttempts(3), WithInitialWait(1*time.Millisecond), WithMaxWait(2*time.Millisecond))

	if err == nil {
		t.Fatal("RunWithRetry() = nil, want error")
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("fn called %d times, want 3", got)
	}
	// Should return the last error (not wrapped in PermanentError)
	if IsPermanent(err) {
		t.Error("exhausted error should not be PermanentError")
	}
}

func TestRunWithRetry_OptionConfigsApply(t *testing.T) {
	transient := fmt.Errorf("startup failed: connection refused")

	var calls atomic.Int32
	start := time.Now()
	err := RunWithRetry(func() error {
		calls.Add(1)
		return transient
	},
		WithMaxAttempts(2),
		WithInitialWait(10*time.Millisecond),
		WithMaxWait(20*time.Millisecond),
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("RunWithRetry() = nil, want error")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("fn called %d times, want 2", got)
	}
	// With 2 attempts, there is 1 wait interval (between attempt 1 and 2).
	// initialWait is 10ms with +-20% jitter = 8ms-12ms.
	// Allow generous bounds for CI variability.
	if elapsed < 5*time.Millisecond {
		t.Errorf("elapsed %v is too short, expected at least ~8ms", elapsed)
	}
}

func TestRunWithRetry_UnknownErrorsAreRetried(t *testing.T) {
	// Unknown errors (not classified as permanent or transient) should be retried
	// per the conservative approach specified in requirements.
	unknown := errors.New("something unexpected happened")

	var calls atomic.Int32
	err := RunWithRetry(func() error {
		n := calls.Add(1)
		if n < 2 {
			return unknown
		}
		return nil
	}, WithInitialWait(1*time.Millisecond))
	if err != nil {
		t.Errorf("RunWithRetry() = %v, want nil", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("fn called %d times, want 2", got)
	}
}

func TestRunWithRetry_WithLogger(t *testing.T) {
	// Verify that WithRetryLogger does not panic and the logger is used.
	// We just verify no panic; log output verification would require a custom handler.
	logger := slog.Default()
	transient := fmt.Errorf("startup failed: connection refused")

	var calls atomic.Int32
	_ = RunWithRetry(func() error {
		n := calls.Add(1)
		if n < 2 {
			return transient
		}
		return nil
	}, WithRetryLogger(logger), WithInitialWait(1*time.Millisecond))

	if got := calls.Load(); got != 2 {
		t.Errorf("fn called %d times, want 2", got)
	}
}

func TestRunWithRetry_ExponentialBackoff(t *testing.T) {
	// Verify that wait times increase exponentially.
	transient := fmt.Errorf("startup failed: connection refused")

	var timestamps []time.Time
	_ = RunWithRetry(func() error {
		timestamps = append(timestamps, time.Now())
		if len(timestamps) < 4 {
			return transient
		}
		return nil
	}, WithMaxAttempts(4), WithInitialWait(20*time.Millisecond), WithMaxWait(200*time.Millisecond))

	if len(timestamps) != 4 {
		t.Fatalf("expected 4 timestamps, got %d", len(timestamps))
	}

	// Between attempt 1 and 2: ~20ms (+-20% jitter = 16-24ms)
	// Between attempt 2 and 3: ~40ms (+-20% jitter = 32-48ms)
	// Between attempt 3 and 4: ~80ms (+-20% jitter = 64-96ms)
	gap1 := timestamps[1].Sub(timestamps[0])
	gap2 := timestamps[2].Sub(timestamps[1])
	gap3 := timestamps[3].Sub(timestamps[2])

	// Second gap should be larger than first (exponential growth)
	if gap2 < gap1 {
		t.Errorf("gap2 (%v) should be >= gap1 (%v) for exponential backoff", gap2, gap1)
	}
	if gap3 < gap2 {
		t.Errorf("gap3 (%v) should be >= gap2 (%v) for exponential backoff", gap3, gap2)
	}
}
