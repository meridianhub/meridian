package persistence

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// baseRepoTestPool is the shared connection pool for base_repository integration tests.
// Initialized once in TestMain and closed on exit.
var baseRepoTestPool *pgxpool.Pool

// TestMain sets up a shared PostgreSQL testcontainer for all base_repository tests.
// Using a shared container avoids the overhead of creating a new container per test.
func TestMain(m *testing.M) {
	ctx := context.Background()

	pgc, err := pgcontainer.Run(ctx,
		"postgres:16-alpine",
		pgcontainer.WithDatabase("test_base_repo"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(60*time.Second)),
	)
	if err != nil {
		os.Exit(1)
	}

	connStr, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgc.Terminate(ctx)
		os.Exit(1)
	}

	poolCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		_ = pgc.Terminate(ctx)
		os.Exit(1)
	}

	baseRepoTestPool, err = pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		_ = pgc.Terminate(ctx)
		os.Exit(1)
	}

	code := m.Run()

	baseRepoTestPool.Close()
	_ = pgc.Terminate(ctx)
	os.Exit(code)
}

func TestNewBaseRepository_StoresPool(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	assert.NotNil(t, repo.pool)
	assert.Equal(t, baseRepoTestPool, repo.pool)
}

func TestBaseRepository_SetSearchPath_NoTenant(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background() // no tenant in context

	tx, err := baseRepoTestPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Without tenant in context, setSearchPath is a no-op
	err = repo.setSearchPath(ctx, tx)
	assert.NoError(t, err)
}

func TestBaseRepository_SetSearchPath_WithTenant(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background()
	tenantID := tenant.MustNewTenantID("test_corp")

	// Create schema for tenant
	_, err := baseRepoTestPool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "org_test_corp"`)
	require.NoError(t, err)

	tenantCtx := tenant.WithTenant(ctx, tenantID)
	tx, err := baseRepoTestPool.Begin(tenantCtx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(tenantCtx) }()

	err = repo.setSearchPath(tenantCtx, tx)
	assert.NoError(t, err)
}

func TestBaseRepository_WithReadTransaction_ExecutesFunction(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background()

	executed := false
	err := repo.withReadTransaction(ctx, func(_ pgx.Tx) error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}

func TestBaseRepository_WithReadTransaction_ReturnsError(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background()

	sentinel := errors.New("read error")
	err := repo.withReadTransaction(ctx, func(_ pgx.Tx) error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestBaseRepository_WithWriteTransaction_ExecutesFunction(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background()

	executed := false
	err := repo.withWriteTransaction(ctx, func(_ pgx.Tx) error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}

func TestBaseRepository_WithWriteTransaction_ReturnsError(t *testing.T) {
	repo := newBaseRepository(baseRepoTestPool)
	ctx := context.Background()

	sentinel := errors.New("write error")
	err := repo.withWriteTransaction(ctx, func(_ pgx.Tx) error {
		return sentinel
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestBaseRepository_WithReadTransaction_WithTenantScope(t *testing.T) {
	ctx := context.Background()
	tenantID := tenant.MustNewTenantID("scoped_corp")

	// Create schema for tenant
	_, err := baseRepoTestPool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "org_scoped_corp"`)
	require.NoError(t, err)

	repo := newBaseRepository(baseRepoTestPool)
	tenantCtx := tenant.WithTenant(ctx, tenantID)

	executed := false
	err = repo.withReadTransaction(tenantCtx, func(_ pgx.Tx) error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}
