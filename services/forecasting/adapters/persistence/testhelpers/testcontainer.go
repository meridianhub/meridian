// Package testhelpers provides shared utilities for repository integration tests.
package testhelpers

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/forecasting/adapters/persistence"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

// TestContainer holds the test database container, connection pool, and repository instance.
type TestContainer struct {
	container *cockroachdb.CockroachDBContainer
	Pool      *pgxpool.Pool
	Repo      *persistence.StrategyRepository
}

// SetupTestContainer creates a CockroachDB testcontainer with the forecasting schema loaded.
func SetupTestContainer(t *testing.T) *TestContainer {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create CockroachDB container in insecure mode
	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_forecasting"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	require.NoError(t, err, "Failed to start CockroachDB container")

	// Get connection config
	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	require.NoError(t, err, "Failed to get connection config")
	connStr := connConfig.ConnString()

	// Create connection pool
	poolConfig, err := pgxpool.ParseConfig(connStr)
	require.NoError(t, err, "Failed to parse pool config")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	require.NoError(t, err, "Failed to create connection pool")

	// Load schema
	loadSchema(t, pool)

	// Create repository
	repo := persistence.NewStrategyRepository(pool)

	return &TestContainer{
		container: crdbContainer,
		Pool:      pool,
		Repo:      repo,
	}
}

// Cleanup closes the connection pool and terminates the container.
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

// loadSchema loads the forecasting schema into the test database.
func loadSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE forecasting_strategy (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			starlark_code TEXT NOT NULL,
			horizon_hours INT NOT NULL CHECK (horizon_hours > 0 AND horizon_hours <= 168),
			granularity_hours INT NOT NULL CHECK (granularity_hours > 0 AND granularity_hours <= horizon_hours),
			schedule TEXT NOT NULL,
			input_dataset_codes TEXT[] NOT NULL,
			output_dataset_code TEXT NOT NULL,
			reference_data_resolution_key TEXT,
			status TEXT NOT NULL DEFAULT 'DRAFT',
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT chk_forecasting_strategy_status CHECK (status IN ('DRAFT', 'ACTIVE', 'DEPRECATED'))
		)
	`)
	require.NoError(t, err, "Failed to create forecasting_strategy table")

	_, err = pool.Exec(ctx, `
		CREATE UNIQUE INDEX idx_forecasting_strategy_unique_active
			ON forecasting_strategy (tenant_id, name)
			WHERE status = 'ACTIVE';

		CREATE INDEX idx_forecasting_strategy_tenant_status
			ON forecasting_strategy (tenant_id, status, created_at DESC);

		CREATE INDEX idx_forecasting_strategy_active
			ON forecasting_strategy (status, tenant_id)
			WHERE status = 'ACTIVE';
	`)
	require.NoError(t, err, "Failed to create indexes")
}
