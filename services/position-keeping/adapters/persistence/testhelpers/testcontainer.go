// Package testhelpers provides shared utilities for repository integration tests.
//
// This package implements a reusable testcontainers setup for PostgreSQL integration tests,
// following the pattern established in postgres_repository_test.go. The testcontainer
// infrastructure provides:
//
//   - Isolated PostgreSQL 16 containers per test
//   - Automatic schema migration and setup
//   - Connection pooling with pgx
//   - Proper cleanup and resource management
//   - Helper functions for common test operations
//
// Usage:
//
//	func TestMyRepository(t *testing.T) {
//	    tc := testhelpers.SetupTestContainer(t)
//	    defer tc.Cleanup(t)
//
//	    // Use tc.Pool for direct database access
//	    // Use tc.Repo for repository operations
//	}
package testhelpers

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestContainer holds the test database container, connection pool, and repository instance.
// It provides a complete testing environment with proper cleanup.
type TestContainer struct {
	container *postgres.PostgresContainer
	Pool      *pgxpool.Pool
	Repo      *persistence.PostgresRepository
}

// SetupTestContainer creates a PostgreSQL testcontainer with the position_keeping schema loaded.
// This function:
//   - Creates an isolated PostgreSQL 16 container
//   - Waits for the database to be ready (up to 30s)
//   - Creates a connection pool with pgx
//   - Loads the complete position_keeping schema
//   - Creates a PostgresRepository instance
//
// The container uses postgres:16-alpine for fast startup and small size.
// Each test gets its own isolated database to prevent test interference.
//
// Example:
//
//	func TestCreate(t *testing.T) {
//	    tc := SetupTestContainer(t)
//	    defer tc.Cleanup(t)
//
//	    log := createTestLog(t, "ACC-001")
//	    err := tc.Repo.Create(context.Background(), log)
//	    require.NoError(t, err)
//	}
func SetupTestContainer(t *testing.T) *TestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container with explicit wait strategy
	// Use both log-based and port-based waiting to prevent race conditions where
	// the container reports readiness before Docker finishes port forwarding (common on macOS/Windows).
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_position_keeping"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(30*time.Second)),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadSchema(t, pool)

	// Create repository
	repo := persistence.NewPostgresRepository(pool)

	return &TestContainer{
		container: pgContainer,
		Pool:      pool,
		Repo:      repo,
	}
}

// Cleanup closes the connection pool and terminates the container.
// This should be called with defer immediately after SetupTestContainer:
//
//	tc := SetupTestContainer(t)
//	defer tc.Cleanup(t)
//
// Cleanup ensures that:
//   - Database connections are properly closed
//   - The PostgreSQL container is stopped and removed
//   - Resources are freed for other tests
func (tc *TestContainer) Cleanup(t *testing.T) {
	t.Helper()
	ctx := context.Background()

	if tc.Pool != nil {
		tc.Pool.Close()
	}

	if tc.container != nil {
		require.NoError(t, tc.container.Terminate(ctx), "Failed to terminate container")
	}
}

// loadSchema loads the complete position_keeping schema into the test database.
// This includes:
//   - position_keeping schema
//   - financial_position_logs table with indexes
//   - transaction_log_entries table with foreign keys
//   - transaction_lineages table with JSONB columns
//   - audit_trail_entries table with JSONB columns
//
// The schema matches the production Atlas migrations but is loaded directly
// for test speed. This avoids the overhead of running migrations in tests.
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create schemas
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS position_keeping`)
	require.NoError(t, err, "Failed to create schema")

	// Create financial_position_logs table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.financial_position_logs (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			log_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			version bigint NOT NULL DEFAULT 1,
			current_status character varying(20) NOT NULL,
			previous_status character varying(20) NULL,
			status_updated_at timestamptz NOT NULL,
			status_reason text NOT NULL,
			failure_reason text NULL,
			reconciliation_status character varying(20) NOT NULL,
			PRIMARY KEY (id)
		)
	`)
	require.NoError(t, err, "Failed to create financial_position_logs table")

	// Create indexes
	_, err = pool.Exec(ctx, `
		CREATE UNIQUE INDEX idx_position_keeping_financial_position_logs_log_id
		ON position_keeping.financial_position_logs (log_id)
	`)
	require.NoError(t, err, "Failed to create log_id index")

	// Create transaction_log_entries table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_log_entries (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			entry_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			account_id character varying(34) NOT NULL,
			amount_cents bigint NOT NULL,
			currency character(3) NOT NULL DEFAULT 'GBP',
			direction character varying(10) NOT NULL,
			timestamp timestamptz NOT NULL,
			description text NULL,
			reference character varying(100) NULL,
			source character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_log_entries_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_logs(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_log_entries table")

	// Create transaction_lineages table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.transaction_lineages (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			financial_position_log_id uuid NOT NULL,
			transaction_id uuid NOT NULL,
			parent_transaction_id uuid NULL,
			child_transaction_ids jsonb NOT NULL DEFAULT '[]',
			related_transaction_ids jsonb NOT NULL DEFAULT '[]',
			transaction_type character varying(50) NOT NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_transaction_lineages_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_logs(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create transaction_lineages table")

	// Create audit_trail_entries table
	_, err = pool.Exec(ctx, `
		CREATE TABLE position_keeping.audit_trail_entries (
			id uuid NOT NULL DEFAULT gen_random_uuid(),
			created_at timestamptz NOT NULL DEFAULT now(),
			created_by character varying(100) NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			updated_by character varying(100) NOT NULL,
			deleted_at timestamptz NULL,
			audit_id uuid NOT NULL,
			financial_position_log_id uuid NOT NULL,
			timestamp timestamptz NOT NULL,
			user_id character varying(100) NOT NULL,
			action character varying(100) NOT NULL,
			details text NULL,
			ip_address character varying(45) NULL,
			system_context jsonb NULL,
			PRIMARY KEY (id),
			CONSTRAINT fk_audit_trail_entries_financial_position_log
				FOREIGN KEY (financial_position_log_id)
				REFERENCES position_keeping.financial_position_logs(id)
				ON DELETE CASCADE
		)
	`)
	require.NoError(t, err, "Failed to create audit_trail_entries table")
}
