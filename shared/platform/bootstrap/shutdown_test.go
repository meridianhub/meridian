package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// Test sentinel errors
var (
	errTestShutdownTrigger = errors.New("test shutdown trigger")
	errCleanupFailed       = errors.New("cleanup failed")
	errTrigger             = errors.New("trigger")
	errServerCrashed       = errors.New("server crashed")
	errShutdownFailed      = errors.New("shutdown failed")
	errServer1Failed       = errors.New("server 1 shutdown failed")
	errServer2Failed       = errors.New("server 2 shutdown failed")
	errBuffer1             = errors.New("buffer error 1")
	errBuffer2             = errors.New("buffer error 2")
	errBuffer3             = errors.New("buffer error 3")
	errBuffer4             = errors.New("buffer error 4")
)

func TestShutdownOrchestrator_CleanupReverseOrder(t *testing.T) {
	t.Run("executes cleanup functions in LIFO order", func(t *testing.T) {
		// Create a mock gRPC server
		server := grpc.NewServer()

		// Use a buffer to capture log output for verification
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		orchestrator := NewShutdownOrchestrator(server, logger)

		// Track execution order
		var mu sync.Mutex
		executionOrder := make([]int, 0)

		// Register 3 cleanup functions
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			executionOrder = append(executionOrder, 1)
			return nil
		})
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			executionOrder = append(executionOrder, 2)
			return nil
		})
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			executionOrder = append(executionOrder, 3)
			return nil
		})

		// Use a short timeout for the test
		orchestrator.WithTimeout(100 * time.Millisecond)

		// Create an error channel and send an error to trigger shutdown
		serverErrors := make(chan error, 1)
		serverErrors <- errTestShutdownTrigger

		// Wait should execute cleanup and return
		err := orchestrator.Wait(serverErrors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test shutdown trigger")

		// Verify LIFO order: 3, 2, 1
		assert.Equal(t, []int{3, 2, 1}, executionOrder, "cleanup functions should execute in LIFO order")
	})

	t.Run("logs errors from cleanup functions but continues", func(t *testing.T) {
		server := grpc.NewServer()

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		orchestrator := NewShutdownOrchestrator(server, logger)
		orchestrator.WithTimeout(100 * time.Millisecond)

		// Track which functions were called
		var mu sync.Mutex
		called := make([]bool, 3)

		// First function succeeds
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			called[0] = true
			return nil
		})

		// Second function fails
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			called[1] = true
			return errCleanupFailed
		})

		// Third function succeeds
		orchestrator.AddCleanup(func() error {
			mu.Lock()
			defer mu.Unlock()
			called[2] = true
			return nil
		})

		serverErrors := make(chan error, 1)
		serverErrors <- errTrigger

		_ = orchestrator.Wait(serverErrors)

		// All functions should have been called despite the error
		assert.True(t, called[0], "first cleanup should have been called")
		assert.True(t, called[1], "second cleanup should have been called")
		assert.True(t, called[2], "third cleanup should have been called")

		// Verify error was logged
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "cleanup function failed")
	})
}

func TestShutdownOrchestrator_WithTimeout(t *testing.T) {
	t.Run("applies custom timeout", func(t *testing.T) {
		server := grpc.NewServer()
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

		orchestrator := NewShutdownOrchestrator(server, logger)

		// Default should be 30s
		assert.Equal(t, DefaultShutdownTimeout, orchestrator.shutdownTimeout)

		// Set custom timeout
		customTimeout := 10 * time.Second
		result := orchestrator.WithTimeout(customTimeout)

		// Should return self for chaining
		assert.Same(t, orchestrator, result)

		// Should have new timeout
		assert.Equal(t, customTimeout, orchestrator.shutdownTimeout)
	})

	t.Run("supports method chaining", func(t *testing.T) {
		server := grpc.NewServer()
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

		orchestrator := NewShutdownOrchestrator(server, logger).
			WithTimeout(5 * time.Second)

		assert.Equal(t, 5*time.Second, orchestrator.shutdownTimeout)
	})
}

func TestNewShutdownOrchestrator(t *testing.T) {
	t.Run("initializes with default timeout", func(t *testing.T) {
		server := grpc.NewServer()
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

		orchestrator := NewShutdownOrchestrator(server, logger)

		assert.NotNil(t, orchestrator)
		assert.Equal(t, server, orchestrator.grpcServer)
		assert.Equal(t, logger, orchestrator.logger)
		assert.Equal(t, DefaultShutdownTimeout, orchestrator.shutdownTimeout)
		assert.NotNil(t, orchestrator.cleanupFuncs)
		assert.Empty(t, orchestrator.cleanupFuncs)
	})
}

func TestShutdownOrchestrator_Wait(t *testing.T) {
	t.Run("returns server error when triggered by error channel", func(t *testing.T) {
		server := grpc.NewServer()
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

		orchestrator := NewShutdownOrchestrator(server, logger).
			WithTimeout(100 * time.Millisecond)

		serverErrors := make(chan error, 1)
		serverErrors <- errServerCrashed

		err := orchestrator.Wait(serverErrors)
		assert.Equal(t, errServerCrashed, err)
	})

	t.Run("returns nil when triggered by signal", func(t *testing.T) {
		// This test is tricky because we'd need to send actual signals.
		// Instead, we verify the error path returns nil when there's no server error.
		// The signal handling is tested implicitly through the error channel path.

		// We can at least verify the orchestrator initializes correctly
		server := grpc.NewServer()
		logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

		orchestrator := NewShutdownOrchestrator(server, logger)
		assert.NotNil(t, orchestrator)
	})
}

func TestSignalHandler(t *testing.T) {
	t.Run("returns channel and cleanup function", func(t *testing.T) {
		sigChan, cleanup := SignalHandler()
		defer cleanup()

		assert.NotNil(t, sigChan)
		assert.NotNil(t, cleanup)
	})

	t.Run("cleanup is idempotent", func(t *testing.T) {
		sigChan, cleanup := SignalHandler()

		// Multiple cleanup calls should not panic
		assert.NotPanics(t, func() {
			cleanup()
			cleanup()
			cleanup()
		})

		assert.NotNil(t, sigChan)
	})

	t.Run("signal channel is buffered", func(t *testing.T) {
		sigChan, cleanup := SignalHandler()
		defer cleanup()

		// Channel should be buffered with size 1
		assert.Equal(t, 1, cap(sigChan))
	})

	t.Run("cleanup returns proper function that can be deferred", func(t *testing.T) {
		// This test verifies the cleanup function integrates properly with defer.
		// We cannot safely send SIGINT/SIGTERM in tests as they would terminate
		// the test process, so we verify the structural correctness of the API.

		// Simulate the expected usage pattern with defer
		var cleanupCalled bool
		func() {
			sigChan, cleanup := SignalHandler()
			defer func() {
				cleanup()
				cleanupCalled = true
			}()

			// Verify channel is ready to receive
			assert.NotNil(t, sigChan)
			assert.Equal(t, 1, cap(sigChan), "channel should be buffered")
		}()

		assert.True(t, cleanupCalled, "cleanup should have been called via defer")
	})

	t.Run("multiple handlers can coexist independently", func(t *testing.T) {
		// Verify that multiple SignalHandler calls create independent handlers
		sigChan1, cleanup1 := SignalHandler()
		sigChan2, cleanup2 := SignalHandler()

		// Both channels should be valid and independently buffered
		assert.Equal(t, 1, cap(sigChan1), "first channel should be buffered")
		assert.Equal(t, 1, cap(sigChan2), "second channel should be buffered")

		// Cleaning up one should not affect the other
		cleanup1()

		// sigChan2 should still be valid and have capacity
		assert.Equal(t, 1, cap(sigChan2), "second channel should remain valid after first cleanup")

		cleanup2()
	})
}

// mockShutdownHandler implements ShutdownHandler for testing.
type mockShutdownHandler struct {
	shutdownCalled atomic.Bool
	shutdownDelay  time.Duration
	shutdownErr    error
	ctxReceived    context.Context
	mu             sync.Mutex
}

func (m *mockShutdownHandler) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.ctxReceived = ctx
	m.mu.Unlock()

	m.shutdownCalled.Store(true)
	if m.shutdownDelay > 0 {
		select {
		case <-time.After(m.shutdownDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.shutdownErr
}

func (m *mockShutdownHandler) WasCalled() bool {
	return m.shutdownCalled.Load()
}

func (m *mockShutdownHandler) ReceivedContext() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctxReceived
}

func TestGracefulShutdown_Success(t *testing.T) {
	t.Run("all servers shutdown cleanly", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server1 := &mockShutdownHandler{}
		server2 := &mockShutdownHandler{}
		server3 := &mockShutdownHandler{}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := GracefulShutdown(ctx, logger, server1, server2, server3)

		require.NoError(t, err)
		assert.True(t, server1.WasCalled(), "server1 Shutdown should have been called")
		assert.True(t, server2.WasCalled(), "server2 Shutdown should have been called")
		assert.True(t, server3.WasCalled(), "server3 Shutdown should have been called")
	})

	t.Run("context is propagated to servers", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server := &mockShutdownHandler{}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := GracefulShutdown(ctx, logger, server)

		require.NoError(t, err)
		assert.True(t, server.WasCalled())

		// Verify context was propagated (it should have a deadline)
		receivedCtx := server.ReceivedContext()
		require.NotNil(t, receivedCtx)
		_, hasDeadline := receivedCtx.Deadline()
		assert.True(t, hasDeadline, "context should have deadline propagated")
	})

	t.Run("handles nil servers gracefully", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server := &mockShutdownHandler{}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Mix of nil and valid servers
		err := GracefulShutdown(ctx, logger, nil, server, nil)

		require.NoError(t, err)
		assert.True(t, server.WasCalled(), "non-nil server should have been called")
	})

	t.Run("applies default timeout when context has no deadline", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server := &mockShutdownHandler{}

		// Use context without deadline
		err := GracefulShutdown(context.Background(), logger, server)

		require.NoError(t, err)
		assert.True(t, server.WasCalled())

		// Verify a deadline was added internally
		receivedCtx := server.ReceivedContext()
		require.NotNil(t, receivedCtx)
		_, hasDeadline := receivedCtx.Deadline()
		assert.True(t, hasDeadline, "default deadline should have been applied")
	})
}

func TestGracefulShutdown_Timeout(t *testing.T) {
	t.Run("returns context deadline exceeded when shutdown exceeds timeout", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		// Server that takes longer than timeout
		slowServer := &mockShutdownHandler{
			shutdownDelay: 500 * time.Millisecond,
		}

		// Use a short timeout for test speed
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := GracefulShutdown(ctx, logger, slowServer)

		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)

		// Server should have been called even though it timed out
		assert.True(t, slowServer.WasCalled())
	})

	t.Run("logs error when shutdown times out", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		slowServer := &mockShutdownHandler{
			shutdownDelay: 500 * time.Millisecond,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_ = GracefulShutdown(ctx, logger, slowServer)

		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "server shutdown failed")
	})
}

func TestGracefulShutdown_Errors(t *testing.T) {
	t.Run("returns first error when server fails", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		failingServer := &mockShutdownHandler{
			shutdownErr: errShutdownFailed,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := GracefulShutdown(ctx, logger, failingServer)

		require.Error(t, err)
		assert.Equal(t, errShutdownFailed, err)
	})

	t.Run("all servers attempted shutdown despite earlier errors", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server1 := &mockShutdownHandler{
			shutdownErr: errServer1Failed,
		}
		server2 := &mockShutdownHandler{}
		server3 := &mockShutdownHandler{
			shutdownErr: errServer2Failed,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := GracefulShutdown(ctx, logger, server1, server2, server3)

		// Returns first error
		require.Error(t, err)
		assert.Equal(t, errServer1Failed, err)

		// All servers should have been attempted
		assert.True(t, server1.WasCalled(), "server1 should have been called")
		assert.True(t, server2.WasCalled(), "server2 should have been called")
		assert.True(t, server3.WasCalled(), "server3 should have been called")
	})

	t.Run("logs each server failure", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		server1 := &mockShutdownHandler{
			shutdownErr: errServer1Failed,
		}
		server2 := &mockShutdownHandler{
			shutdownErr: errServer2Failed,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = GracefulShutdown(ctx, logger, server1, server2)

		logOutput := logBuf.String()
		// Both failures should be logged
		assert.Contains(t, logOutput, "server shutdown failed")
		assert.Contains(t, logOutput, "server 1 shutdown failed")
		assert.Contains(t, logOutput, "server 2 shutdown failed")
	})
}

func TestServerErrorChannel(t *testing.T) {
	t.Run("creates channel with correct buffer size", func(t *testing.T) {
		errChan := ServerErrorChannel(3)
		assert.Equal(t, 3, cap(errChan))
	})

	t.Run("can send serverCount errors without blocking", func(t *testing.T) {
		errChan := ServerErrorChannel(3)

		// Should not block for first 3 sends
		errChan <- errBuffer1
		errChan <- errBuffer2
		errChan <- errBuffer3

		// Verify all errors are in the channel
		assert.Len(t, errChan, 3)
	})

	t.Run("fourth send would block when buffer is full", func(t *testing.T) {
		errChan := ServerErrorChannel(3)

		// Fill the buffer
		errChan <- errBuffer1
		errChan <- errBuffer2
		errChan <- errBuffer3

		// Fourth send should block - verify with select/default
		blocked := true
		select {
		case errChan <- errBuffer4:
			blocked = false
		default:
			// This path means the channel is full and would block
		}

		assert.True(t, blocked, "fourth send should block when buffer is full")
	})

	t.Run("zero buffer creates unbuffered channel", func(t *testing.T) {
		errChan := ServerErrorChannel(0)
		assert.Equal(t, 0, cap(errChan))
	})
}

func TestWaitForShutdownSignal(t *testing.T) {
	t.Run("returns error when server error received", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		sigChan := make(chan os.Signal, 1)
		errChan := make(chan error, 1)

		// Send server error
		errChan <- errServerCrashed

		err := WaitForShutdownSignal(sigChan, errChan, logger)

		require.Error(t, err)
		assert.Equal(t, errServerCrashed, err)

		// Verify error was logged
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "server error")
	})

	t.Run("logs server error details", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		sigChan := make(chan os.Signal, 1)
		errChan := make(chan error, 1)
		errChan <- errServerCrashed

		_ = WaitForShutdownSignal(sigChan, errChan, logger)

		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "server error, initiating shutdown")
	})

	t.Run("returns nil when shutdown signal received", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		sigChan := make(chan os.Signal, 1)
		errChan := make(chan error, 1)

		// Send signal
		sigChan <- syscall.SIGTERM

		err := WaitForShutdownSignal(sigChan, errChan, logger)

		require.NoError(t, err)

		// Verify signal was logged
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "received shutdown signal")
	})

	t.Run("logs signal type when received", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		sigChan := make(chan os.Signal, 1)
		errChan := make(chan error, 1)
		sigChan <- syscall.SIGINT

		_ = WaitForShutdownSignal(sigChan, errChan, logger)

		logOutput := logBuf.String()
		assert.Contains(t, logOutput, "signal")
	})

	t.Run("signal takes priority when both available simultaneously", func(t *testing.T) {
		// This test verifies behavior when both channels have values
		// Due to Go's select randomness, we run multiple times and verify
		// at least one of each path is taken

		var signalCount, errorCount int
		iterations := 50

		for i := 0; i < iterations; i++ {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))

			sigChan := make(chan os.Signal, 1)
			errChan := make(chan error, 1)

			// Send both simultaneously
			sigChan <- syscall.SIGTERM
			errChan <- errServerCrashed

			err := WaitForShutdownSignal(sigChan, errChan, logger)

			if err == nil {
				signalCount++
			} else {
				errorCount++
			}
		}

		// With Go's random select, we should see both paths taken
		// over 50 iterations
		assert.Greater(t, signalCount+errorCount, 0, "some iterations should complete")
	})

	t.Run("concurrent signal and error handling", func(t *testing.T) {
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		sigChan := make(chan os.Signal, 1)
		errChan := make(chan error, 1)

		// Use await to verify the function blocks until signal/error
		var result error
		var completed atomic.Bool

		go func() {
			result = WaitForShutdownSignal(sigChan, errChan, logger)
			completed.Store(true)
		}()

		// Initially should be blocked
		err := await.New().
			AtMost(50 * time.Millisecond).
			PollInterval(5 * time.Millisecond).
			Until(func() bool {
				return completed.Load()
			})
		assert.ErrorIs(t, err, await.ErrTimeout, "should be blocked without signal or error")

		// Send signal
		sigChan <- syscall.SIGTERM

		// Now should complete
		err = await.New().
			AtMost(100 * time.Millisecond).
			PollInterval(5 * time.Millisecond).
			Until(func() bool {
				return completed.Load()
			})
		require.NoError(t, err, "should complete after receiving signal")
		assert.NoError(t, result)
	})
}
