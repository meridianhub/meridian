package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupPostgresContainer creates a PostgreSQL container for integration testing
func setupPostgresContainer(t *testing.T) (*PostgresPool, func()) {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "failed to start postgres container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	// Create connection pool
	cfg := DefaultConfig(connStr)
	cfg.MaxConnections = 10
	cfg.MinConnections = 2

	pool, err := NewPostgresPool(ctx, cfg)
	require.NoError(t, err, "failed to create postgres pool")

	// Create test table
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS accounts (
			id SERIAL PRIMARY KEY,
			account_id VARCHAR(50) UNIQUE NOT NULL,
			name VARCHAR(100) NOT NULL,
			balance DECIMAL(15,2) NOT NULL DEFAULT 0.00,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`
	_, err = pool.ExecContext(ctx, createTableSQL)
	require.NoError(t, err, "failed to create test table")

	cleanup := func() {
		_ = pool.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return pool, cleanup
}

func TestPostgresPool_Integration_QueryContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test data
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-001", "Test Account", 1000.00)
	require.NoError(t, err)

	// Query the data
	rows, err := pool.QueryContext(ctx,
		"SELECT account_id, name, balance FROM accounts WHERE account_id = $1",
		"ACC-001")
	require.NoError(t, err)
	defer rows.Close()

	// Verify results
	require.True(t, rows.Next(), "expected at least one row")

	var accountID, name string
	var balance float64
	err = rows.Scan(&accountID, &name, &balance)
	require.NoError(t, err)

	assert.Equal(t, "ACC-001", accountID)
	assert.Equal(t, "Test Account", name)
	assert.Equal(t, 1000.00, balance)

	require.False(t, rows.Next(), "expected only one row")
	require.NoError(t, rows.Err())
}

func TestPostgresPool_Integration_ExecContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("INSERT operation", func(t *testing.T) {
		result, err := pool.ExecContext(ctx,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"ACC-INSERT", "Insert Test", 500.00)
		require.NoError(t, err)

		rowsAffected, err := result.RowsAffected()
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)
	})

	t.Run("UPDATE operation", func(t *testing.T) {
		// First insert
		_, err := pool.ExecContext(ctx,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"ACC-UPDATE", "Update Test", 1000.00)
		require.NoError(t, err)

		// Then update
		result, err := pool.ExecContext(ctx,
			"UPDATE accounts SET balance = $1 WHERE account_id = $2",
			2000.00, "ACC-UPDATE")
		require.NoError(t, err)

		rowsAffected, err := result.RowsAffected()
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)

		// Verify update
		var balance float64
		err = pool.QueryRowContext(ctx,
			"SELECT balance FROM accounts WHERE account_id = $1",
			"ACC-UPDATE").Scan(&balance)
		require.NoError(t, err)
		assert.Equal(t, 2000.00, balance)
	})

	t.Run("DELETE operation", func(t *testing.T) {
		// First insert
		_, err := pool.ExecContext(ctx,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"ACC-DELETE", "Delete Test", 1000.00)
		require.NoError(t, err)

		// Then delete
		result, err := pool.ExecContext(ctx,
			"DELETE FROM accounts WHERE account_id = $1",
			"ACC-DELETE")
		require.NoError(t, err)

		rowsAffected, err := result.RowsAffected()
		require.NoError(t, err)
		assert.Equal(t, int64(1), rowsAffected)

		// Verify deletion
		var count int
		err = pool.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
			"ACC-DELETE").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestPostgresPool_Integration_Transaction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert initial accounts
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-FROM", "From Account", 1000.00)
	require.NoError(t, err)

	_, err = pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-TO", "To Account", 0.00)
	require.NoError(t, err)

	// Test successful transaction using WithTransaction helper
	err = WithTransaction(ctx, pool, func(tx DB) error {
		// Debit from source account
		_, err := tx.ExecContext(ctx,
			"UPDATE accounts SET balance = balance - $1 WHERE account_id = $2",
			500.00, "ACC-FROM")
		if err != nil {
			return err
		}

		// Credit to destination account
		_, err = tx.ExecContext(ctx,
			"UPDATE accounts SET balance = balance + $1 WHERE account_id = $2",
			500.00, "ACC-TO")
		return err
	})
	require.NoError(t, err)

	// Verify balances after transaction
	var fromBalance, toBalance float64
	err = pool.QueryRowContext(ctx,
		"SELECT balance FROM accounts WHERE account_id = $1",
		"ACC-FROM").Scan(&fromBalance)
	require.NoError(t, err)
	assert.Equal(t, 500.00, fromBalance)

	err = pool.QueryRowContext(ctx,
		"SELECT balance FROM accounts WHERE account_id = $1",
		"ACC-TO").Scan(&toBalance)
	require.NoError(t, err)
	assert.Equal(t, 500.00, toBalance)
}

func TestPostgresPool_Integration_TransactionRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert initial account
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-ROLLBACK", "Rollback Test", 1000.00)
	require.NoError(t, err)

	// Test transaction rollback on error
	err = WithTransaction(ctx, pool, func(tx DB) error {
		// Update balance
		_, err := tx.ExecContext(ctx,
			"UPDATE accounts SET balance = balance - $1 WHERE account_id = $2",
			500.00, "ACC-ROLLBACK")
		if err != nil {
			return err
		}

		// Simulate error - return error to trigger rollback
		return fmt.Errorf("simulated error")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated error")

	// Verify balance unchanged after rollback
	var balance float64
	err = pool.QueryRowContext(ctx,
		"SELECT balance FROM accounts WHERE account_id = $1",
		"ACC-ROLLBACK").Scan(&balance)
	require.NoError(t, err)
	assert.Equal(t, 1000.00, balance, "balance should be unchanged after rollback")
}

func TestPostgresPool_Integration_TransactionIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test account
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-ISOLATION", "Isolation Test", 1000.00)
	require.NoError(t, err)

	// Test serializable isolation
	err = WithTransactionOptions(ctx, pool, &TxOptions{
		Isolation: Serializable,
		ReadOnly:  false,
	}, func(tx DB) error {
		// Read initial balance
		var balance float64
		err := tx.QueryRowContext(ctx,
			"SELECT balance FROM accounts WHERE account_id = $1",
			"ACC-ISOLATION").Scan(&balance)
		if err != nil {
			return err
		}

		// Update balance
		_, err = tx.ExecContext(ctx,
			"UPDATE accounts SET balance = $1 WHERE account_id = $2",
			balance+500.00, "ACC-ISOLATION")
		return err
	})
	require.NoError(t, err)

	// Verify final balance
	var balance float64
	err = pool.QueryRowContext(ctx,
		"SELECT balance FROM accounts WHERE account_id = $1",
		"ACC-ISOLATION").Scan(&balance)
	require.NoError(t, err)
	assert.Equal(t, 1500.00, balance)
}

func TestPostgresPool_Integration_ConcurrentQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test accounts
	for i := 1; i <= 10; i++ {
		_, err := pool.ExecContext(ctx,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			fmt.Sprintf("ACC-%03d", i), fmt.Sprintf("Account %d", i), float64(i*100))
		require.NoError(t, err)
	}

	// Run concurrent queries
	concurrency := 20
	iterations := 10
	var wg sync.WaitGroup
	errors := make(chan error, concurrency*iterations)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				accountID := fmt.Sprintf("ACC-%03d", (workerID%10)+1)

				// Query account
				var balance float64
				err := pool.QueryRowContext(ctx,
					"SELECT balance FROM accounts WHERE account_id = $1",
					accountID).Scan(&balance)
				if err != nil {
					errors <- fmt.Errorf("worker %d iteration %d: %w", workerID, j, err)
					return
				}

				// Update account
				_, err = pool.ExecContext(ctx,
					"UPDATE accounts SET balance = balance + $1 WHERE account_id = $2",
					10.00, accountID)
				if err != nil {
					errors <- fmt.Errorf("worker %d iteration %d: %w", workerID, j, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorList := []error{}
	for err := range errors {
		errorList = append(errorList, err)
	}
	assert.Empty(t, errorList, "concurrent queries should not produce errors")
}

func TestPostgresPool_Integration_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	t.Run("query with timeout", func(t *testing.T) {
		// Create context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()

		// Wait a bit to ensure timeout
		time.Sleep(10 * time.Millisecond)

		// Query should fail due to timeout
		_, err := pool.QueryContext(ctx,
			"SELECT account_id, name, balance FROM accounts")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context")
	})

	t.Run("transaction with timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := WithTransaction(ctx, pool, func(tx DB) error {
			// Simulate slow operation
			time.Sleep(200 * time.Millisecond)
			return nil
		})
		assert.Error(t, err)
	})

	t.Run("manual cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Cancel immediately
		cancel()

		_, err := pool.QueryContext(ctx,
			"SELECT account_id, name, balance FROM accounts")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context")
	})
}

func TestPostgresPool_Integration_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	t.Run("Ping returns no error", func(t *testing.T) {
		err := pool.Ping(ctx)
		assert.NoError(t, err)
	})

	t.Run("Ping after close returns error", func(t *testing.T) {
		// Create separate pool for this test
		tempPool, tempCleanup := setupPostgresContainer(t)
		defer tempCleanup()

		// Close the pool
		err := tempPool.Close()
		require.NoError(t, err)

		// Ping should fail
		err = tempPool.Ping(ctx)
		assert.Error(t, err)
	})
}

func TestPostgresPool_Integration_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test data
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-SHUTDOWN", "Shutdown Test", 1000.00)
	require.NoError(t, err)

	// Start some queries
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var balance float64
			_ = pool.QueryRowContext(ctx,
				"SELECT balance FROM accounts WHERE account_id = $1",
				"ACC-SHUTDOWN").Scan(&balance)
		}()
	}

	// Wait for queries to complete
	wg.Wait()

	// Close should complete gracefully
	err = pool.Close()
	assert.NoError(t, err)

	// Operations after close should fail
	_, err = pool.QueryContext(ctx,
		"SELECT account_id FROM accounts")
	assert.Error(t, err)
}

func TestPostgresPool_Integration_QueryRowContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test data
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-QUERYROW", "Query Row Test", 1000.00)
	require.NoError(t, err)

	t.Run("existing row", func(t *testing.T) {
		var accountID, name string
		var balance float64

		err := pool.QueryRowContext(ctx,
			"SELECT account_id, name, balance FROM accounts WHERE account_id = $1",
			"ACC-QUERYROW").Scan(&accountID, &name, &balance)
		require.NoError(t, err)

		assert.Equal(t, "ACC-QUERYROW", accountID)
		assert.Equal(t, "Query Row Test", name)
		assert.Equal(t, 1000.00, balance)
	})

	t.Run("non-existing row", func(t *testing.T) {
		var accountID string

		err := pool.QueryRowContext(ctx,
			"SELECT account_id FROM accounts WHERE account_id = $1",
			"ACC-NONEXISTENT").Scan(&accountID)
		assert.Error(t, err)
		assert.Equal(t, sql.ErrNoRows, err)
	})
}

func TestPostgresPool_Integration_NestedTransactionError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Attempting nested transaction should return error
	err := WithTransaction(ctx, pool, func(tx DB) error {
		// Try to start another transaction within the transaction
		_, err := tx.BeginTx(ctx, nil)
		return err
	})

	require.Error(t, err)
	assert.Equal(t, ErrNestedTransaction, err)
}

func TestPostgresPool_Integration_ReadOnlyTransaction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	pool, cleanup := setupPostgresContainer(t)
	defer cleanup()

	ctx := context.Background()

	// Insert test data
	_, err := pool.ExecContext(ctx,
		"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-READONLY", "Read Only Test", 1000.00)
	require.NoError(t, err)

	// Read-only transaction should allow queries
	var balance float64
	err = WithTransactionOptions(ctx, pool, &TxOptions{
		Isolation: Serializable,
		ReadOnly:  true,
	}, func(tx DB) error {
		return tx.QueryRowContext(ctx,
			"SELECT balance FROM accounts WHERE account_id = $1",
			"ACC-READONLY").Scan(&balance)
	})
	require.NoError(t, err)
	assert.Equal(t, 1000.00, balance)

	// Read-only transaction should reject writes
	err = WithTransactionOptions(ctx, pool, &TxOptions{
		Isolation: Serializable,
		ReadOnly:  true,
	}, func(tx DB) error {
		_, err := tx.ExecContext(ctx,
			"UPDATE accounts SET balance = $1 WHERE account_id = $2",
			2000.00, "ACC-READONLY")
		return err
	})
	assert.Error(t, err)
}
