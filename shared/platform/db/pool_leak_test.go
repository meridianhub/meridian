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
