package await

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test errors
var (
	errNotReadyYet    = errors.New("not ready yet")
	errPersistentTest = errors.New("persistent error")
)

func TestUntil_ImmediateSuccess(t *testing.T) {
	err := New().Until(func() bool {
		return true
	})
	require.NoError(t, err)
}

func TestUntil_EventualSuccess(t *testing.T) {
	var counter int32
	err := New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return atomic.AddInt32(&counter, 1) >= 3
		})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&counter), int32(3))
}

func TestUntil_Timeout(t *testing.T) {
	err := New().
		AtMost(100 * time.Millisecond).
		PollInterval(20 * time.Millisecond).
		Until(func() bool {
			return false
		})
	require.ErrorIs(t, err, ErrTimeout)
}

func TestUntil_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := New().
		WithContext(ctx).
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return false
		})
	require.ErrorIs(t, err, ErrContextCancelled)
}

func TestUntilNoError_Success(t *testing.T) {
	var attempts int32
	err := New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			if atomic.AddInt32(&attempts, 1) < 3 {
				return errNotReadyYet
			}
			return nil
		})
	require.NoError(t, err)
}

func TestUntilNoError_Timeout(t *testing.T) {
	err := New().
		AtMost(100 * time.Millisecond).
		PollInterval(20 * time.Millisecond).
		UntilNoError(func() error {
			return errPersistentTest
		})
	// Should return the last error, not ErrTimeout
	require.ErrorIs(t, err, errPersistentTest)
}

func TestUntilValue_Success(t *testing.T) {
	var attempts int32
	result, err := UntilValue(
		New().AtMost(1*time.Second).PollInterval(10*time.Millisecond),
		func() *string {
			if atomic.AddInt32(&attempts, 1) < 3 {
				return nil
			}
			s := "found"
			return &s
		},
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "found", *result)
}

func TestUntilValue_Timeout(t *testing.T) {
	result, err := UntilValue(
		New().AtMost(100*time.Millisecond).PollInterval(20*time.Millisecond),
		func() *string {
			return nil
		},
	)
	require.ErrorIs(t, err, ErrTimeout)
	require.Nil(t, result)
}

func TestConvenienceFunctions(t *testing.T) {
	t.Run("Until", func(t *testing.T) {
		err := Until(func() bool { return true })
		require.NoError(t, err)
	})

	t.Run("UntilNoError", func(t *testing.T) {
		err := UntilNoError(func() error { return nil })
		require.NoError(t, err)
	})

	t.Run("AtMost", func(t *testing.T) {
		a := AtMost(5 * time.Second)
		assert.Equal(t, 5*time.Second, a.timeout)
	})

	t.Run("PollEvery", func(t *testing.T) {
		a := PollEvery(50 * time.Millisecond)
		assert.Equal(t, 50*time.Millisecond, a.pollInterval)
	})
}

func TestDefaults(t *testing.T) {
	a := New()
	assert.Equal(t, DefaultTimeout, a.timeout)
	assert.Equal(t, DefaultPollInterval, a.pollInterval)
}

func TestFluentChaining(t *testing.T) {
	ctx := context.Background()
	a := New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		WithContext(ctx)

	assert.Equal(t, 5*time.Second, a.timeout)
	assert.Equal(t, 50*time.Millisecond, a.pollInterval)
	assert.Equal(t, ctx, a.ctx)
}

// TestRealWorldScenario demonstrates a practical use case
func TestRealWorldScenario_WaitForState(t *testing.T) {
	// Simulate an async operation that changes state
	type Order struct {
		Status string
	}
	order := &Order{Status: "PENDING"}

	// Simulate async status change
	go func() {
		time.Sleep(50 * time.Millisecond)
		order.Status = "PROCESSING"
		time.Sleep(50 * time.Millisecond)
		order.Status = "COMPLETED"
	}()

	// Wait for completed status
	err := New().
		AtMost(500 * time.Millisecond).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return order.Status == "COMPLETED"
		})

	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", order.Status)
}
