package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestMain enables goleak verification for all tests in this package.
// This catches goroutine leaks that would otherwise go unnoticed.
//
// Note: Some tests in this package intentionally test cancellation scenarios
// that may leave database/sql internal goroutines running until explicit cleanup.
// We ignore these known goroutines as they are cleaned up by deferred cleanup
// functions and are not indicative of leaks in our code.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Ignore database/sql internal goroutines that may persist briefly
		// during context cancellation tests. These are cleaned up by deferred
		// cleanup functions but may still be running at test completion.
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
		// Ignore testcontainers Reaper goroutines. The Reaper is a global singleton
		// in the testcontainers library that runs a background goroutine to clean up
		// containers. It is not a leak in our code.
		goleak.IgnoreTopFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
	)
}

// TestPoolCloseWithContext_NoLeaksOnSuccess verifies that CloseWithContext
// does not leak goroutines during normal successful close operation.
// This is the primary leak detection test - it ensures the errgroup-based
// implementation properly cleans up all goroutines on the happy path.
func TestPoolCloseWithContext_NoLeaksOnSuccess(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CloseWithContext should succeed without leaking goroutines
	err = pool.CloseWithContext(ctx)
	require.NoError(t, err, "unexpected error during close")

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolCloseWithContext_CancelledContextReturnsError verifies that
// CloseWithContext returns an error when the context is already cancelled,
// and properly cleans up without leaking goroutines.
func TestPoolCloseWithContext_CancelledContextReturnsError(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db (sql.Open without connecting)
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// CloseWithContext should return promptly with cancelled context
	err = pool.CloseWithContext(ctx)
	require.Error(t, err, "expected error from cancelled context")
	require.Contains(t, err.Error(), "cancelled", "error should indicate cancellation")

	// Even though CloseWithContext returned an error due to cancelled context,
	// we still need to close the underlying db to avoid leaking the connectionOpener goroutine.
	// This is expected behavior - the caller should handle cleanup on error.
	// For testing purposes, we close manually to verify no leak from CloseWithContext itself.
	_ = db.Close()

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolCloseWithContext_MultipleCloseAttempts verifies that calling
// CloseWithContext multiple times (first cancelled, then successful) does
// not leak goroutines and handles the double-close gracefully.
func TestPoolCloseWithContext_MultipleCloseAttempts(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	// First attempt with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = pool.CloseWithContext(cancelledCtx)

	// Second attempt with valid context should complete the close
	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	err = pool.CloseWithContext(ctx)
	// May succeed or error (db already closed) - either is acceptable
	_ = err

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolClose_NoLeaks verifies that the simple Close() method
// does not leak goroutines.
func TestPoolClose_NoLeaks(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	// Simple Close should not leak
	err = pool.Close()
	require.NoError(t, err, "unexpected error during close")

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolCloseWithContext_TimeoutDuringClose verifies behavior when context
// times out during an ongoing Close operation. This simulates a "mid-execution
// cancellation" scenario.
//
// Note: Since sql.DB.Close() is typically fast and cannot be mocked easily,
// we test with a very short timeout to create a race between context cancellation
// and close completion. The test verifies that regardless of which wins,
// the implementation doesn't leak goroutines.
func TestPoolCloseWithContext_TimeoutDuringClose(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	// Use extremely short timeout to create a race condition between
	// context cancellation and Close() completion
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Intentional sleep: Ensure context expires before/during Close to test race handling
	time.Sleep(1 * time.Millisecond)

	// CloseWithContext may succeed or fail depending on timing
	// Either outcome is acceptable - we're testing for goroutine leaks
	err = pool.CloseWithContext(ctx)
	// If context timed out before close completed, we need to ensure cleanup
	if err != nil {
		// Ensure the underlying db is closed to prevent leaks
		_ = db.Close()
	}

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolCloseWithContext_ConcurrentCloses verifies that multiple concurrent
// CloseWithContext calls don't cause races or leaks.
// This test should be run with -race flag.
func TestPoolCloseWithContext_ConcurrentCloses(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	// Launch multiple concurrent close attempts
	const numGoroutines = 10
	done := make(chan struct{}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			_ = pool.CloseWithContext(ctx) // Error expected for all but first close
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// goleak.VerifyNone will fail if any goroutines leaked
}

// TestPoolCloseWithContext_ContextCancelledMidway simulates context cancellation
// occurring while the close operation is in progress by using a context that
// will be cancelled after a short delay.
func TestPoolCloseWithContext_ContextCancelledMidway(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	// Create a pool with a mock db
	db, err := sql.Open("pgx", "postgresql://user:pass@localhost:5432/db")
	require.NoError(t, err, "failed to open mock db")

	pool := &PostgresPool{db: db}

	ctx, cancel := context.WithCancel(context.Background())

	// Start close in a goroutine
	closeComplete := make(chan error, 1)
	go func() {
		closeComplete <- pool.CloseWithContext(ctx)
	}()

	// Cancel context immediately after starting close
	// This creates a race between close completion and context cancellation
	cancel()

	// Wait for close to complete
	err = <-closeComplete
	// Either outcome is acceptable:
	// - nil: Close completed before cancellation was detected
	// - error: Cancellation was detected first
	if err != nil {
		// Ensure cleanup if context won the race
		_ = db.Close()
	}

	// goleak.VerifyNone will fail if any goroutines leaked
}
