package db_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGuardedPool(t *testing.T) (*db.GuardedPgxPool, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.NewTestPool(t)
	guarded := db.NewGuardedPgxPool(pool)
	return guarded, pool
}

func TestGuardedPgxPool_ExecRejectsWithoutTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	_, err := guarded.Exec(context.Background(), "SELECT 1")

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_ExecAllowsWithTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))

	_, err := guarded.Exec(ctx, "SELECT 1")

	require.NoError(t, err)
}

func TestGuardedPgxPool_ExecAllowsWithBypass(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := db.WithPgxTenantBypass(context.Background())

	_, err := guarded.Exec(ctx, "SELECT 1")

	require.NoError(t, err)
}

func TestGuardedPgxPool_QueryRejectsWithoutTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	_, err := guarded.Query(context.Background(), "SELECT 1")

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_QueryAllowsWithTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))

	rows, err := guarded.Query(ctx, "SELECT 1")
	require.NoError(t, err)
	rows.Close()
}

func TestGuardedPgxPool_QueryRowRejectsWithoutTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	row := guarded.QueryRow(context.Background(), "SELECT 1")

	var result int
	err := row.Scan(&result)

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_QueryRowAllowsWithTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))

	var result int
	err := guarded.QueryRow(ctx, "SELECT 1").Scan(&result)

	require.NoError(t, err)
	assert.Equal(t, 1, result)
}

func TestGuardedPgxPool_BeginRejectsWithoutTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	_, err := guarded.Begin(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_BeginAllowsWithTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("test_tenant"))

	tx, err := guarded.Begin(ctx)
	require.NoError(t, err)
	_ = tx.Rollback(ctx)
}

func TestGuardedPgxPool_BeginTenantTx_RejectsWithoutTenant(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	_, err := guarded.BeginTenantTx(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_BeginTenantTx_SetsSearchPath(t *testing.T) {
	t.Parallel()
	guarded, pool := setupGuardedPool(t)

	tid := tenant.TenantID("acme_bank")
	ctx := tenant.WithTenant(context.Background(), tid)

	// Create the tenant schema so SET search_path works
	bypassCtx := db.WithPgxTenantBypass(context.Background())
	_, err := pool.Exec(bypassCtx, "CREATE SCHEMA IF NOT EXISTS org_acme_bank")
	require.NoError(t, err)

	tx, err := guarded.BeginTenantTx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify search_path is set correctly
	var searchPath string
	err = tx.QueryRow(ctx, "SHOW search_path").Scan(&searchPath)
	require.NoError(t, err)
	assert.Contains(t, searchPath, "org_acme_bank")
}

func TestGuardedPgxPool_BeginTenantTx_RejectsEmptyTenantID(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(""))

	_, err := guarded.BeginTenantTx(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestGuardedPgxPool_Pool_ReturnsUnderlyingPool(t *testing.T) {
	t.Parallel()
	guarded, pool := setupGuardedPool(t)

	assert.Same(t, pool, guarded.Pool())
}

func TestGuardedPgxPool_ImplementsPgxQuerier(t *testing.T) {
	t.Parallel()
	guarded, _ := setupGuardedPool(t)

	// Compile-time check is in guarded_pgx_pool.go, but verify at runtime too.
	var _ db.PgxQuerier = guarded
}
