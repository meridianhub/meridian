package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresPool_Integration_CloseWithContext tests graceful close with context
func TestPostgresPool_Integration_CloseWithContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()

	t.Run("successful close within timeout", func(t *testing.T) {
		// Create a new pool for this test
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// Close with generous timeout
		closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := tempPool.CloseWithContext(closeCtx)
		assert.NoError(t, err)
	})

	t.Run("close with cancelled context", func(t *testing.T) {
		// Create a new pool for this test
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// Create already-cancelled context
		closeCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		err := tempPool.CloseWithContext(closeCtx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "close operation cancelled")
	})

	t.Run("close with timeout while connections active", func(t *testing.T) {
		// Create a new pool for this test
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// Very short timeout that might expire during close
		closeCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
		defer cancel()

		//nolint:forbidigo // ensures nanosecond-TTL context has expired before calling CloseWithContext
		time.Sleep(10 * time.Millisecond)

		err := tempPool.CloseWithContext(closeCtx)
		// Either succeeds quickly or fails with timeout
		if err != nil {
			assert.Contains(t, err.Error(), "close operation cancelled")
		}
	})
}

// TestPostgresPool_Integration_DrainConnections tests connection draining with timeout
func TestPostgresPool_Integration_DrainConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("drain idle connections", func(t *testing.T) {
		// All connections should be idle
		drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := pool.DrainConnections(drainCtx, 100*time.Millisecond)
		assert.NoError(t, err)
	})

	t.Run("drain with timeout while connections in use", func(t *testing.T) {
		// Create a new pool for this test
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// Start a long-running query
		queryCtx, queryCancel := context.WithCancel(ctx)
		defer queryCancel()
		go func() {
			rows, err := tempPool.QueryContext(queryCtx, "SELECT pg_sleep(2)")
			if err != nil {
				t.Logf("Query failed during drain test (expected if context cancelled): %v", err)
				return
			}
			if rows != nil {
				defer func() { _ = rows.Close() }()
				_ = rows.Err() // Check for errors after iteration
			}
		}()

		// Wait for query to start (connection becomes in-use)
		awaitErr := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
			stats := tempPool.Stats()
			return stats.InUse > 0
		})
		require.NoError(t, awaitErr, "query should start and acquire a connection")

		// Try to drain with short timeout
		drainCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()

		err := tempPool.DrainConnections(drainCtx, 100*time.Millisecond)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "drain timeout")
		assert.Contains(t, err.Error(), "connections still in use")
	})

	t.Run("drain with cancelled context and active connections", func(t *testing.T) {
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// Start a long-running query to keep connections active
		queryCtx, queryCancel := context.WithCancel(ctx)
		defer queryCancel()
		go func() {
			rows, err := tempPool.QueryContext(queryCtx, "SELECT pg_sleep(5)")
			if err != nil {
				t.Logf("Query failed during cancelled context test (expected if context cancelled): %v", err)
				return
			}
			if rows != nil {
				defer func() { _ = rows.Close() }()
				_ = rows.Err() // Check for errors after iteration
			}
		}()

		// Wait for query to start (connection becomes in-use)
		awaitErr := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
			stats := tempPool.Stats()
			return stats.InUse > 0
		})
		require.NoError(t, awaitErr, "query should start and acquire a connection")

		// Create and immediately cancel context
		drainCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := tempPool.DrainConnections(drainCtx, 100*time.Millisecond)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})
}

// TestPostgresPool_Integration_Stats tests pool statistics
func TestPostgresPool_Integration_Stats(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("stats returns valid data", func(t *testing.T) {
		stats := pool.Stats()

		// Should have max connections configured
		assert.Equal(t, 25, stats.MaxOpenConnections)

		// OpenConnections should be >= 0
		assert.GreaterOrEqual(t, stats.OpenConnections, 0)

		// InUse should be >= 0
		assert.GreaterOrEqual(t, stats.InUse, 0)

		// Idle should be >= 0
		assert.GreaterOrEqual(t, stats.Idle, 0)
	})

	t.Run("stats after query execution", func(t *testing.T) {
		// Execute a query
		_, err := pool.ExecContext(ctx, "SELECT 1")
		require.NoError(t, err)

		stats := pool.Stats()
		assert.GreaterOrEqual(t, stats.OpenConnections, 0)
	})
}

// TestPostgresPool_Integration_ExecContextError tests error path for ExecContext
func TestPostgresPool_Integration_ExecContextError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("exec with syntax error", func(t *testing.T) {
		result, err := pool.ExecContext(ctx, "INVALID SQL SYNTAX")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "exec failed")
	})
}

// TestPostgresPool_Integration_BeginTxError tests error path for BeginTx
func TestPostgresPool_Integration_BeginTxError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("begin tx with cancelled context", func(t *testing.T) {
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		tx, err := pool.BeginTx(cancelledCtx, nil)
		assert.Error(t, err)
		assert.Nil(t, tx)
		assert.Contains(t, err.Error(), "failed to begin transaction")
	})
}

// TestPostgresPool_Integration_CloseError tests error path for Close
func TestPostgresPool_Integration_CloseError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()

	t.Run("close triggers cleanup", func(t *testing.T) {
		// Create a separate pool for this test
		tempPool, tempCleanup := setupPostgresContainer(ctx, t)
		defer tempCleanup()

		// First close should succeed
		err := tempPool.Close()
		assert.NoError(t, err)

		// Operations after close should fail
		err = tempPool.Ping(ctx)
		assert.Error(t, err)
	})
}

// TestTx_QueryContext tests error path for transaction QueryContext
func TestTx_Integration_QueryContextError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("query with invalid SQL in transaction", func(t *testing.T) {
		err := WithTransaction(ctx, pool, func(tx DB) error {
			rows, err := tx.QueryContext(ctx, "INVALID SQL")
			if rows != nil {
				defer func() { _ = rows.Close() }()
				_ = rows.Err() // Check for errors after iteration
			}
			if err != nil {
				return fmt.Errorf("failed to execute query in transaction: %w", err)
			}
			return nil
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "transaction query failed")
	})
}

// TestTx_Ping tests Ping error on transaction
func TestTx_Integration_Ping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("ping on transaction returns error", func(t *testing.T) {
		err := WithTransaction(ctx, pool, func(tx DB) error {
			return tx.Ping(ctx)
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ping not supported")
	})
}

// TestTx_Close tests Close on transaction (should be no-op)
func TestTx_Integration_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("close on transaction is no-op", func(t *testing.T) {
		err := WithTransaction(ctx, pool, func(tx DB) error {
			return tx.Close()
		})
		assert.NoError(t, err)
	})
}

// TestToSQLOptions_Unit tests conversion of TxOptions to sql.TxOptions
func TestToSQLOptions_Unit(t *testing.T) {
	t.Run("nil options uses defaults", func(t *testing.T) {
		sqlOpts := toSQLOptions(nil)
		assert.Equal(t, sql.LevelSerializable, sqlOpts.Isolation)
		assert.False(t, sqlOpts.ReadOnly)
	})

	t.Run("ReadCommitted isolation", func(t *testing.T) {
		opts := &TxOptions{Isolation: ReadCommitted}
		sqlOpts := toSQLOptions(opts)
		assert.Equal(t, sql.LevelReadCommitted, sqlOpts.Isolation)
	})

	t.Run("RepeatableRead isolation", func(t *testing.T) {
		opts := &TxOptions{Isolation: RepeatableRead}
		sqlOpts := toSQLOptions(opts)
		assert.Equal(t, sql.LevelRepeatableRead, sqlOpts.Isolation)
	})

	t.Run("Serializable isolation", func(t *testing.T) {
		opts := &TxOptions{Isolation: Serializable}
		sqlOpts := toSQLOptions(opts)
		assert.Equal(t, sql.LevelSerializable, sqlOpts.Isolation)
	})

	t.Run("ReadOnly flag", func(t *testing.T) {
		opts := &TxOptions{ReadOnly: true}
		sqlOpts := toSQLOptions(opts)
		assert.True(t, sqlOpts.ReadOnly)
	})

	t.Run("invalid isolation defaults to Serializable", func(t *testing.T) {
		opts := &TxOptions{Isolation: IsolationLevel(999)}
		sqlOpts := toSQLOptions(opts)
		assert.Equal(t, sql.LevelSerializable, sqlOpts.Isolation)
	})
}

// TestHealthChecker_Integration tests HealthChecker functionality
func TestHealthChecker_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupPostgresContainer(ctx, t)
	defer cleanup()

	t.Run("GetStats returns pool statistics", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 1 * time.Second,
			CheckTimeout:  500 * time.Millisecond,
		})

		stats := checker.GetStats()
		assert.Equal(t, 25, stats.MaxOpenConnections)
		assert.GreaterOrEqual(t, stats.OpenConnections, 0)
	})

	t.Run("Check performs synchronous health check", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 1 * time.Second,
			CheckTimeout:  500 * time.Millisecond,
		})

		err := checker.Check(ctx)
		assert.NoError(t, err)
	})

	t.Run("Check fails with cancelled context", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 1 * time.Second,
			CheckTimeout:  500 * time.Millisecond,
		})

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := checker.Check(cancelledCtx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "health check failed")
	})

	t.Run("PeriodicHealthCheck runs background checks", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 100 * time.Millisecond,
			CheckTimeout:  50 * time.Millisecond,
		})

		// Start periodic checks
		go checker.PeriodicHealthCheck()

		// Wait for first check to complete
		err := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
			return checker.IsHealthy()
		})
		require.NoError(t, err, "checker should become healthy after first check")

		// Should have last check time
		lastCheckTime := checker.GetLastCheckTime()
		assert.False(t, lastCheckTime.IsZero())

		// Should have no error
		assert.NoError(t, checker.GetLastCheckError())

		// Stop the checker
		checker.Stop()
	})

	t.Run("PeriodicHealthCheck stops on context cancellation", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 100 * time.Millisecond,
			CheckTimeout:  50 * time.Millisecond,
		})

		// Start periodic checks
		go checker.PeriodicHealthCheck()

		// Wait for at least one check
		err := await.AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
			return checker.IsHealthy()
		})
		require.NoError(t, err, "checker should become healthy after first check")

		// Stop should complete quickly
		done := make(chan bool)
		go func() {
			checker.Stop()
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatal("Stop() did not complete within timeout")
		}
	})

	t.Run("IsHealthy returns false when no checks performed", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 1 * time.Second,
			CheckTimeout:  500 * time.Millisecond,
		})

		// Before any checks
		assert.False(t, checker.IsHealthy())
	})

	t.Run("IsHealthy returns false after stale checks", func(t *testing.T) {
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			CheckInterval: 10 * time.Millisecond,
			CheckTimeout:  5 * time.Millisecond,
		})

		// Start and wait for one check
		go checker.PeriodicHealthCheck()
		err := await.AtMost(2 * time.Second).PollInterval(5 * time.Millisecond).Until(func() bool {
			return checker.IsHealthy()
		})
		require.NoError(t, err, "checker should become healthy after first check")
		checker.Stop()

		//nolint:forbidigo // triggers health check staleness detection; requires actual time to pass beyond 2x interval
		time.Sleep(30 * time.Millisecond)

		// Should now be unhealthy due to staleness
		assert.False(t, checker.IsHealthy())
	})
}

// setupPostgresContainer is already defined in integration_test.go
// but we need to ensure it's available for these tests
