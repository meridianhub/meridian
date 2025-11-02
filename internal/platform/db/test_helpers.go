package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTestPool creates a PostgreSQL testcontainer and connection pool for testing.
// This is a shared helper used by both health_test.go and integration_test.go.
func setupTestPool(t *testing.T) (*PostgresPool, func()) {
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

	// Create connection pool with smaller limits for tests
	cfg := DefaultConfig(connStr)
	cfg.MaxConnections = 5
	cfg.MinConnections = 1

	pool, err := NewPostgresPool(ctx, cfg)
	require.NoError(t, err, "failed to create postgres pool")

	cleanup := func() {
		_ = pool.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return pool, cleanup
}
