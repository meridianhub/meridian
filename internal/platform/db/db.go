// Package db provides a database abstraction layer optimized for distributed SQL databases.
//
// This package implements a unified DB interface that works seamlessly with both connection
// pools and transactions, enabling repository code to be written once and work in either context.
// The design is optimized for CockroachDB and YugabyteDB with support for:
// - Serializable isolation by default
// - Automatic transaction retry logic
// - Connection pooling with health checks
// - Context-aware operations for timeout and cancellation
//
// The core DB interface is implemented by both PostgresPool (connection pooling) and Tx
// (transaction wrapper), allowing the same repository methods to work with either.
package db

import (
	"context"
	"database/sql"
	"time"
)

// DB is the core database interface that abstracts database operations.
// This interface is implemented by both connection pools and transactions,
// allowing repository code to work seamlessly with either.
//
// Design principles:
// - Context-aware: All operations accept context for timeouts and cancellation
// - Transaction support: BeginTx enables explicit transaction management
// - Standard library compatible: Uses database/sql types for broad compatibility
type DB interface {
	// QueryContext executes a query that returns rows, typically a SELECT.
	// The caller must call rows.Close() when finished.
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)

	// ExecContext executes a query without returning any rows (INSERT, UPDATE, DELETE).
	// The result contains information about the execution (rows affected, last insert ID).
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)

	// QueryRowContext executes a query that returns at most one row.
	// It automatically handles closing the underlying rows.
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row

	// BeginTx starts a new transaction with the specified options.
	// Returns ErrNestedTransaction if called on a transaction (nested transactions not supported).
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)

	// Ping verifies the database connection is alive.
	// Returns an error if the connection is not healthy.
	Ping(ctx context.Context) error

	// Close closes the database connection or releases resources.
	// For connection pools, this closes all connections.
	// For transactions, this is typically a no-op (use Commit/Rollback instead).
	Close() error
}

// Config holds the configuration for database connection pooling.
type Config struct {
	// ConnectionString is the PostgreSQL connection URL (e.g., "postgresql://user:pass@host:26257/db?sslmode=require")
	ConnectionString string

	// MaxConnections is the maximum number of connections in the pool (default: 50)
	MaxConnections int

	// MinConnections is the minimum number of idle connections to maintain (default: 5)
	MinConnections int

	// ConnectionTimeout is the maximum time to wait for a connection from the pool (default: 30 seconds)
	ConnectionTimeout time.Duration

	// HealthCheckInterval is how often to verify connections are healthy (default: 30 seconds)
	HealthCheckInterval time.Duration

	// MaxConnectionLifetime is the maximum duration a connection can be reused (default: 1 hour)
	MaxConnectionLifetime time.Duration

	// MaxConnectionIdleTime is the maximum time a connection can remain idle (default: 10 minutes)
	MaxConnectionIdleTime time.Duration

	// StatementTimeout is the maximum duration for individual SQL statements (default: 30 seconds)
	StatementTimeout time.Duration
}

// DefaultConfig returns a Config with sensible defaults for CockroachDB/YugabyteDB.
func DefaultConfig(connectionString string) *Config {
	return &Config{
		ConnectionString:      connectionString,
		MaxConnections:        50,
		MinConnections:        5,
		ConnectionTimeout:     30 * time.Second,
		HealthCheckInterval:   30 * time.Second,
		MaxConnectionLifetime: 1 * time.Hour,
		MaxConnectionIdleTime: 10 * time.Minute,
		StatementTimeout:      30 * time.Second,
	}
}
