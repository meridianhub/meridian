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
	_ "embed"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

//go:embed schema.sql
var schemaDDL string

// TestContainer holds the test database container, connection pool, and repository instance.
// It provides a complete testing environment with proper cleanup.
type TestContainer struct {
	container       *postgres.PostgresContainer
	Pool            *pgxpool.Pool
	Repo            *persistence.PostgresRepository
	PositionRepo    *persistence.PositionRepository
	ReservationRepo *persistence.ReservationRepository
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

	// Get connection string with search_path configured
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable", "search_path=position_keeping,public")
	require.NoError(t, err, "Failed to get connection string")

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadSchema(t, pool)

	// Create repositories
	repo := persistence.NewPostgresRepository(pool)
	positionRepo := persistence.NewPositionRepository(pool)
	reservationRepo := persistence.NewReservationRepository(pool)

	return &TestContainer{
		container:       pgContainer,
		Pool:            pool,
		Repo:            repo,
		PositionRepo:    positionRepo,
		ReservationRepo: reservationRepo,
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

// loadSchema loads the complete position_keeping schema into the test database
// from the embedded schema.sql file. The schema matches the production Atlas
// migrations but is loaded directly for test speed.
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), schemaDDL)
	require.NoError(t, err, "Failed to load embedded schema DDL")
}
