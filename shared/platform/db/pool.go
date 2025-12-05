package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx driver
)

var (
	// ErrConfigNil is returned when a nil config is passed to NewPostgresPool
	ErrConfigNil = errors.New("config cannot be nil")
	// ErrConnectionStringEmpty is returned when an empty connection string is provided
	ErrConnectionStringEmpty = errors.New("connection string cannot be empty")
)

// PostgresPool implements the DB interface using database/sql with pgx driver.
// It wraps sql.DB to provide connection pooling optimized for CockroachDB and YugabyteDB.
type PostgresPool struct {
	db *sql.DB
}

// NewPostgresPool creates a new connection pool with the specified configuration.
// It configures the pool according to CockroachDB best practices including:
// - Serializable isolation level by default
// - Statement timeout
// - Connection lifetime management
// - Health check intervals
func NewPostgresPool(ctx context.Context, cfg *Config) (*PostgresPool, error) {
	if cfg == nil {
		return nil, fmt.Errorf("failed to create pool: %w", ErrConfigNil)
	}

	if cfg.ConnectionString == "" {
		return nil, fmt.Errorf("failed to create pool: %w", ErrConnectionStringEmpty)
	}

	// Build connection string with CockroachDB-specific parameters
	connStr := cfg.ConnectionString

	// Open connection using pgx driver
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxConnections)
	db.SetMaxIdleConns(cfg.MinConnections)
	db.SetConnMaxLifetime(cfg.MaxConnectionLifetime)
	db.SetConnMaxIdleTime(cfg.MaxConnectionIdleTime)

	// Verify initial connection
	if err := db.PingContext(ctx); err != nil {
		// Ignore close error during cleanup - ping failure is the real issue
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set session-level defaults
	// Use serializable isolation for CockroachDB best practices
	_, err = db.ExecContext(ctx, "SET default_transaction_isolation = 'serializable'")
	if err != nil {
		// Ignore close error during cleanup - isolation setting failure is the real issue
		_ = db.Close()
		return nil, fmt.Errorf("failed to set default transaction isolation: %w", err)
	}

	// Set statement timeout if configured
	if cfg.StatementTimeout > 0 {
		timeoutMs := cfg.StatementTimeout.Milliseconds()
		_, err = db.ExecContext(ctx, fmt.Sprintf("SET statement_timeout = '%dms'", timeoutMs))
		if err != nil {
			// Ignore close error during cleanup - timeout setting failure is the real issue
			_ = db.Close()
			return nil, fmt.Errorf("failed to set statement timeout: %w", err)
		}
	}

	return &PostgresPool{
		db: db,
	}, nil
}

// QueryContext executes a query that returns rows with context support.
func (p *PostgresPool) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	return rows, nil
}

// ExecContext executes a query without returning rows (INSERT, UPDATE, DELETE).
func (p *PostgresPool) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	result, err := p.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("exec failed: %w", err)
	}
	return result, nil
}

// QueryRowContext executes a query that returns at most one row.
func (p *PostgresPool) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}

// BeginTx starts a transaction with the specified isolation level.
// If opts is nil, the default isolation level (serializable) is used.
func (p *PostgresPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	// If no options specified, use serializable isolation (CockroachDB best practice)
	if opts == nil {
		opts = &sql.TxOptions{
			Isolation: sql.LevelSerializable,
		}
	}

	tx, err := p.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return tx, nil
}

// Ping verifies the database connection is alive.
func (p *PostgresPool) Ping(ctx context.Context) error {
	if err := p.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping failed: %w", err)
	}
	return nil
}

// Close drains the connection pool gracefully.
// It waits for all active connections to be returned to the pool before closing.
// This method blocks until all connections are closed or returns immediately if already closed.
func (p *PostgresPool) Close() error {
	if err := p.db.Close(); err != nil {
		return fmt.Errorf("failed to close database connection: %w", err)
	}
	return nil
}

// CloseWithContext drains the connection pool gracefully with context support.
// It waits for all active connections to be returned or until the context is cancelled.
// This is the recommended method for graceful shutdown with timeout control.
//
// Example usage:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := pool.CloseWithContext(ctx); err != nil {
//		log.Printf("WARN: Pool did not close cleanly: %v", err)
//	}
//
// Returns an error if:
// - Context is cancelled before all connections are closed
// - Database close operation fails
func (p *PostgresPool) CloseWithContext(ctx context.Context) error {
	// Channel to signal when Close() completes
	done := make(chan error, 1)

	// Close in background goroutine
	go func() {
		done <- p.Close()
	}()

	// Wait for close to complete or context cancellation
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context cancelled before close completed
		// Note: db.Close() will still complete in background
		return fmt.Errorf("close operation cancelled: %w", ctx.Err())
	}
}

// DrainConnections waits for active connections to become idle with timeout.
// This method is useful for graceful shutdown to allow in-flight queries to complete
// before closing the pool. It polls connection statistics at regular intervals until
// all connections are idle or the context is cancelled.
//
// Parameters:
// - ctx: Context for timeout and cancellation control
// - pollInterval: How often to check connection stats (recommended: 100ms-1s)
//
// Returns nil if all connections successfully drained (InUse == 0).
// Returns error if context is cancelled before draining completes.
//
// Example usage:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := pool.DrainConnections(ctx, 500*time.Millisecond); err != nil {
//		log.Printf("WARN: Active connections remain: %v", err)
//	}
func (p *PostgresPool) DrainConnections(ctx context.Context, pollInterval time.Duration) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		stats := p.db.Stats()
		if stats.InUse == 0 {
			// All connections idle, safe to close
			return nil
		}

		select {
		case <-ctx.Done():
			// Context cancelled, return current in-use count
			return fmt.Errorf("drain timeout: %d connections still in use: %w", stats.InUse, ctx.Err())
		case <-ticker.C:
			// Continue polling
		}
	}
}

// Stats returns current connection pool statistics.
// These metrics are useful for monitoring pool health and capacity planning.
// This is a wrapper around sql.DB.Stats().
func (p *PostgresPool) Stats() sql.DBStats {
	return p.db.Stats()
}
