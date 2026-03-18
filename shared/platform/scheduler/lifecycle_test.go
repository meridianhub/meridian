package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkerLifecycle(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	assert.NotNil(t, lc)
	assert.False(t, lc.IsRunning(), "new lifecycle should not be running")
}

func TestWorkerLifecycle_Start_ExecutesWorkFunc(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	executed := make(chan struct{})
	err := lc.Start(context.Background(), func(_ context.Context) error {
		close(executed)
		return nil
	})

	require.NoError(t, err)
	select {
	case <-executed:
		// workFunc was called
	case <-time.After(time.Second):
		t.Fatal("workFunc was not executed within timeout")
	}
}

func TestWorkerLifecycle_Start_ReturnsErrAlreadyRunning(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})
	// Start a blocking workFunc
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Second Start should fail
	err := lc.Start(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.ErrorIs(t, err, ErrAlreadyRunning)

	lc.Stop(time.Second)
}

func TestWorkerLifecycle_Start_Idempotent_AfterStop(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	// Start and stop
	err := lc.Start(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err)

	lc.Stop(time.Second)

	// Starting again after stop should return ErrAlreadyStopped
	err = lc.Start(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.ErrorIs(t, err, ErrAlreadyStopped)
}

func TestWorkerLifecycle_Stop_Idempotent(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Multiple concurrent Stop calls should not panic
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lc.Stop(time.Second)
		}()
	}
	wg.Wait()

	assert.False(t, lc.IsRunning(), "should not be running after stop")
}

func TestWorkerLifecycle_Stop_WaitsForInFlightWork(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	workDone := make(chan struct{})
	started := make(chan struct{})

	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Start guarded work that takes time
	go lc.ExecuteGuarded(func() {
		//nolint:forbidigo // Intentional: simulates in-flight work duration for shutdown test
		time.Sleep(200 * time.Millisecond)
		close(workDone)
	})

	//nolint:forbidigo // Intentional: lifecycle has no observable state to poll for "guard registered"
	time.Sleep(50 * time.Millisecond)

	// Stop should wait for guarded work to finish
	lc.Stop(5 * time.Second)

	select {
	case <-workDone:
		// Guarded work completed before Stop returned
	default:
		t.Fatal("Stop returned before in-flight guarded work completed")
	}
}

func TestWorkerLifecycle_Stop_TimesOut(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})

	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Start guarded work that takes longer than timeout
	go lc.ExecuteGuarded(func() {
		//nolint:forbidigo // Intentional: simulates work that outlasts the shutdown timeout
		time.Sleep(5 * time.Second)
	})

	//nolint:forbidigo // Intentional: lifecycle has no observable state to poll for "guard registered"
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	lc.Stop(200 * time.Millisecond)
	elapsed := time.Since(start)

	// Stop should return after timeout, not wait for the 5s work
	assert.Less(t, elapsed, time.Second, "Stop should timeout and return quickly")
}

func TestWorkerLifecycle_ExecuteGuarded_RespectsStoppedFlag(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started
	lc.Stop(time.Second)

	// After stop, ExecuteGuarded should not execute the function
	executed := false
	lc.ExecuteGuarded(func() {
		executed = true
	})

	assert.False(t, executed, "ExecuteGuarded should not execute after Stop")
}

func TestWorkerLifecycle_ExecuteGuarded_TracksWaitGroup(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	var counter atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lc.ExecuteGuarded(func() {
				counter.Add(1)
				//nolint:forbidigo // Intentional: simulates concurrent work duration for WaitGroup tracking test
				time.Sleep(50 * time.Millisecond)
			})
		}()
	}

	wg.Wait()
	lc.Stop(time.Second)

	assert.Equal(t, int32(10), counter.Load(), "all 10 guarded functions should have executed")
}

func TestWorkerLifecycle_Done_ClosedOnStop(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Done should not be closed yet
	select {
	case <-lc.Done():
		t.Fatal("Done channel should not be closed before Stop")
	default:
		// expected
	}

	lc.Stop(time.Second)

	// Done should be closed after Stop
	select {
	case <-lc.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("Done channel should be closed after Stop")
	}
}

func TestWorkerLifecycle_ConcurrentStartStop(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	// Start lifecycle
	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started

	// Concurrent starts and stops should not race
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = lc.Start(context.Background(), func(_ context.Context) error {
				return nil
			})
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lc.Stop(time.Second)
		}()
	}

	wg.Wait()

	assert.False(t, lc.IsRunning(), "should not be running after concurrent stop calls")
}

func TestWorkerLifecycle_IsRunning(t *testing.T) {
	lc := NewWorkerLifecycle(slog.Default())

	assert.False(t, lc.IsRunning(), "should not be running initially")

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started
	assert.True(t, lc.IsRunning(), "should be running after Start")

	lc.Stop(time.Second)
	assert.False(t, lc.IsRunning(), "should not be running after Stop")
}

func TestWorkerLifecycle_NilLogger(t *testing.T) {
	// Passing nil logger should use slog.Default()
	lc := NewWorkerLifecycle(nil)
	assert.NotNil(t, lc)
}

func TestWorkerLifecycle_ZeroValue_SafeToUse(t *testing.T) {
	// Zero value should be safe - fields lazily initialized
	var lc WorkerLifecycle

	assert.False(t, lc.IsRunning(), "zero value should not be running")

	// Done should return a valid channel (not nil)
	done := lc.Done()
	assert.NotNil(t, done, "Done should return non-nil channel on zero value")

	// Stop on zero value should not panic
	lc.Stop(time.Second)

	// Start on stopped zero value should return ErrAlreadyStopped
	err := lc.Start(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.ErrorIs(t, err, ErrAlreadyStopped)
}

func TestWorkerLifecycle_ZeroValue_StartAndStop(t *testing.T) {
	var lc WorkerLifecycle

	started := make(chan struct{})
	go func() {
		_ = lc.Start(context.Background(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()

	<-started
	assert.True(t, lc.IsRunning())

	lc.Stop(time.Second)
	assert.False(t, lc.IsRunning())
}
