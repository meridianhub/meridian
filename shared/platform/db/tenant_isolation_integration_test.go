package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// isolationTestEntity is a GORM model used exclusively in cross-tenant isolation tests.
// Using a distinct type avoids conflicts with the testEntity defined in tenant_guard_test.go.
type isolationTestEntity struct {
	ID      uint   `gorm:"primarykey"`
	Payload string `gorm:"column:payload"`
}

// setupCockroachDBWithTenantSchemas creates a CockroachDB container with two isolated
// tenant schemas, each containing an isolation_test_entities table pre-populated with
// distinct data. Returns the GORM DB and a cleanup function.
func setupCockroachDBWithTenantSchemas(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)

	bypassCtx := db.WithTenantGuardBypass(context.Background())

	tenants := []struct {
		id      string
		payload string
	}{
		{id: "tenant_alpha", payload: "alpha-secret-data"},
		{id: "tenant_beta", payload: "beta-secret-data"},
	}

	for _, td := range tenants {
		schema := tenant.MustNewTenantID(td.id).SchemaName()
		require.NoError(t,
			gormDB.WithContext(bypassCtx).Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error,
			"failed to create schema %s", schema,
		)
		require.NoError(t,
			gormDB.WithContext(bypassCtx).Exec(fmt.Sprintf(`
				CREATE TABLE IF NOT EXISTS %q.isolation_test_entities (
					id   INT PRIMARY KEY DEFAULT unique_rowid(),
					payload TEXT NOT NULL
				)
			`, schema)).Error,
			"failed to create table in schema %s", schema,
		)
		require.NoError(t,
			gormDB.WithContext(bypassCtx).Exec(fmt.Sprintf(
				"INSERT INTO %q.isolation_test_entities (payload) VALUES ($1)",
				schema,
			), td.payload).Error,
			"failed to seed data in schema %s", schema,
		)
	}

	return gormDB, cleanup
}

// TestTenantGuard_Integration_BlocksCreateWithoutScope verifies that TenantGuard rejects
// Create operations executed without an active tenant scope.
func TestTenantGuard_Integration_BlocksCreateWithoutScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	entity := isolationTestEntity{Payload: "should-be-blocked"}
	err := gormDB.WithContext(context.Background()).Create(&entity).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

// TestTenantGuard_Integration_BlocksQueryWithoutScope verifies that TenantGuard rejects
// Query operations executed without an active tenant scope.
func TestTenantGuard_Integration_BlocksQueryWithoutScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	var entities []isolationTestEntity
	err := gormDB.WithContext(context.Background()).Find(&entities).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

// TestTenantGuard_Integration_BlocksUpdateWithoutScope verifies that TenantGuard rejects
// Update operations executed without an active tenant scope.
func TestTenantGuard_Integration_BlocksUpdateWithoutScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	err := gormDB.WithContext(context.Background()).
		Model(&isolationTestEntity{}).
		Where("id = ?", 1).
		Update("payload", "updated").Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

// TestTenantGuard_Integration_BlocksDeleteWithoutScope verifies that TenantGuard rejects
// Delete operations executed without an active tenant scope.
func TestTenantGuard_Integration_BlocksDeleteWithoutScope(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	err := gormDB.WithContext(context.Background()).
		Where("id = ?", 1).
		Delete(&isolationTestEntity{}).Error

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired)
}

// TestTenantGuard_Integration_TenantACannotReadTenantBData verifies that a query executed
// within tenant A's scope cannot access data belonging to tenant B.
func TestTenantGuard_Integration_TenantACannotReadTenantBData(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := setupCockroachDBWithTenantSchemas(t)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	// Query using tenant_alpha's scope — should only see alpha's rows.
	tenantAlpha := tenant.MustNewTenantID("tenant_alpha")
	ctxAlpha := tenant.WithTenant(context.Background(), tenantAlpha)

	var results []struct{ Payload string }
	err := db.WithGormTenantTransaction(ctxAlpha, gormDB, func(tx *gorm.DB) error {
		return tx.Table("isolation_test_entities").Find(&results).Error
	})
	require.NoError(t, err)

	for _, r := range results {
		assert.Equal(t, "alpha-secret-data", r.Payload,
			"tenant_alpha must not see tenant_beta rows; got payload %q", r.Payload)
	}

	// Confirm that beta's payload is not present anywhere in the result set.
	for _, r := range results {
		assert.NotEqual(t, "beta-secret-data", r.Payload,
			"tenant_alpha result set must not contain tenant_beta data")
	}
}

// TestTenantGuard_Integration_TenantACannotModifyTenantBData verifies that write operations
// scoped to tenant A cannot affect tenant B's data.
func TestTenantGuard_Integration_TenantACannotModifyTenantBData(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := setupCockroachDBWithTenantSchemas(t)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	// Insert a row scoped to tenant_alpha.
	tenantAlpha := tenant.MustNewTenantID("tenant_alpha")
	ctxAlpha := tenant.WithTenant(context.Background(), tenantAlpha)

	err := db.WithGormTenantTransaction(ctxAlpha, gormDB, func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO isolation_test_entities (payload) VALUES ($1)", "alpha-new-row").Error
	})
	require.NoError(t, err)

	// Verify the new row is NOT visible in tenant_beta's scope.
	tenantBeta := tenant.MustNewTenantID("tenant_beta")
	ctxBeta := tenant.WithTenant(context.Background(), tenantBeta)

	var count int64
	err = db.WithGormTenantTransaction(ctxBeta, gormDB, func(tx *gorm.DB) error {
		return tx.Table("isolation_test_entities").
			Where("payload = ?", "alpha-new-row").
			Count(&count).Error
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), count,
		"tenant_beta must not see the row inserted by tenant_alpha")
}

// TestTenantGuard_Integration_BypassWorksForMigrations verifies that WithTenantGuardBypass
// allows system-level operations (e.g., schema provisioning, migrations) to execute without
// a tenant scope, while TenantGuard is still active.
func TestTenantGuard_Integration_BypassWorksForMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	bypassCtx := db.WithTenantGuardBypass(context.Background())

	// A DDL statement executed with the bypass context must succeed even though
	// no tenant scope has been set.
	err := gormDB.WithContext(bypassCtx).
		Exec("CREATE TABLE IF NOT EXISTS bypass_test_table (id INT PRIMARY KEY DEFAULT unique_rowid())").Error
	require.NoError(t, err, "bypass context should allow system-level DDL without tenant scope")

	// Verify the table is reachable via bypass context (simulating a migration health-check).
	var count int64
	err = gormDB.WithContext(bypassCtx).
		Table("bypass_test_table").
		Count(&count).Error
	require.NoError(t, err, "bypass context should allow reads on system tables")
	assert.Equal(t, int64(0), count)

	// Confirm that the same query WITHOUT bypass is rejected.
	err = gormDB.WithContext(context.Background()).
		Table("bypass_test_table").
		Count(&count).Error
	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrTenantScopeRequired,
		"non-bypass context must still be blocked by TenantGuard")
}

// TestTenantGuard_Integration_SearchPathRevertsAfterTransaction verifies that the
// PostgreSQL search_path (set via SET LOCAL inside the transaction) automatically reverts
// to its original value after the transaction commits, preventing schema leakage.
func TestTenantGuard_Integration_SearchPathRevertsAfterTransaction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := setupCockroachDBWithTenantSchemas(t)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	bypassCtx := db.WithTenantGuardBypass(context.Background())

	// Capture the baseline search_path before any tenant transaction.
	var before string
	require.NoError(t,
		gormDB.WithContext(bypassCtx).Raw("SHOW search_path").Scan(&before).Error,
		"failed to read baseline search_path",
	)

	// Run a tenant-scoped transaction for tenant_alpha.
	tenantAlpha := tenant.MustNewTenantID("tenant_alpha")
	ctxAlpha := tenant.WithTenant(context.Background(), tenantAlpha)

	var duringTx string
	err := db.WithGormTenantTransaction(ctxAlpha, gormDB, func(tx *gorm.DB) error {
		// Use a bypass sub-context so we can read the search_path from inside the tx
		// without the guard intercepting this diagnostic query.
		return tx.WithContext(db.WithTenantGuardBypass(ctxAlpha)).
			Raw("SHOW search_path").Scan(&duringTx).Error
	})
	require.NoError(t, err)
	assert.Contains(t, duringTx, "org_tenant_alpha",
		"search_path should point to tenant_alpha schema during the transaction")

	// After the transaction commits the search_path must be back to the baseline.
	var after string
	require.NoError(t,
		gormDB.WithContext(bypassCtx).Raw("SHOW search_path").Scan(&after).Error,
		"failed to read post-transaction search_path",
	)
	assert.Equal(t, before, after,
		"search_path must revert to the pre-transaction value after commit")
}

// TestTenantGuard_Integration_ConcurrentTenantRequests verifies that simultaneous
// operations scoped to different tenants do not interfere with one another and each
// tenant sees only its own data.
func TestTenantGuard_Integration_ConcurrentTenantRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	gormDB, cleanup := setupCockroachDBWithTenantSchemas(t)
	defer cleanup()

	require.NoError(t, gormDB.Use(db.NewTenantGuard()))

	type result struct {
		tenantID string
		payload  string
		err      error
	}

	ch := make(chan result, 2)

	runQuery := func(id string, expected string) {
		tid := tenant.MustNewTenantID(id)
		ctx := tenant.WithTenant(context.Background(), tid)

		var rows []struct{ Payload string }
		err := db.WithGormTenantTransaction(ctx, gormDB, func(tx *gorm.DB) error {
			// Introduce a small delay to increase the chance of interleaving.
			time.Sleep(10 * time.Millisecond)
			return tx.Table("isolation_test_entities").Find(&rows).Error
		})

		if err != nil {
			ch <- result{tenantID: id, err: err}
			return
		}

		for _, r := range rows {
			if r.Payload != expected {
				ch <- result{
					tenantID: id,
					payload:  r.Payload,
					err:      fmt.Errorf("tenant %s saw unexpected payload %q (want %q)", id, r.Payload, expected),
				}
				return
			}
		}
		ch <- result{tenantID: id}
	}

	go runQuery("tenant_alpha", "alpha-secret-data")
	go runQuery("tenant_beta", "beta-secret-data")

	for range 2 {
		r := <-ch
		require.NoError(t, r.err, "concurrent tenant query for %s failed", r.tenantID)
	}
}
