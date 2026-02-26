package persistence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if sharedDB == nil {
		t.Skip("Skipping integration test (shared DB not initialized)")
	}

	truncateAllTables(t, sharedDB)

	return sharedDB, func() { /* container lifecycle managed by TestMain */ }
}

func tenantCtx() context.Context {
	tid := tenant.TenantID("test-tenant-01")
	return tenant.WithTenant(context.Background(), tid)
}

// seedRunAndVariance creates prerequisite settlement_run, snapshot, and variance records.
func seedRunAndVariance(t *testing.T, db *gorm.DB, runID, snapshotID, varianceID uuid.UUID) {
	t.Helper()
	ctx := tenantCtx()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())

	// Insert settlement_run
	err := db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_run" (run_id, account_id, scope, settlement_type, period_start, period_end, initiated_by) VALUES (?, 'ACC-001', 'ACCOUNT', 'DAILY', NOW() - INTERVAL '1 day', NOW(), 'system')`, quoted),
		runID,
	).Error
	require.NoError(t, err)

	// Insert settlement_snapshot
	err = db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_snapshot" (snapshot_id, run_id, account_id, instrument_code, expected_balance, actual_balance, variance_amount, source_system, captured_at) SELECT ?, sr.id, 'ACC-001', 'GBP', 100.00, 90.00, -10.00, 'test', NOW() FROM %s."settlement_run" sr WHERE sr.run_id = ?`, quoted, quoted),
		snapshotID, runID,
	).Error
	require.NoError(t, err)

	// Insert variance
	err = db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."variance" (variance_id, run_id, snapshot_id, account_id, instrument_code, expected_amount, actual_amount, variance_amount, reason) SELECT ?, sr.id, ss.id, 'ACC-001', 'GBP', 100.00, 90.00, -10.00, 'AMOUNT_MISMATCH' FROM %s."settlement_run" sr JOIN %s."settlement_snapshot" ss ON ss.run_id = sr.id WHERE sr.run_id = ? AND ss.snapshot_id = ?`, quoted, quoted, quoted),
		varianceID, runID, snapshotID,
	).Error
	require.NoError(t, err)
}

func TestDisputeRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Amount mismatch", "user-1")
	require.NoError(t, err)

	err = repo.Create(ctx, dispute)
	require.NoError(t, err)

	// Verify retrieval
	found, err := repo.FindByID(ctx, dispute.DisputeID)
	require.NoError(t, err)
	assert.Equal(t, dispute.DisputeID, found.DisputeID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, domain.DisputeStatusOpen, found.Status)
	assert.Equal(t, "Amount mismatch", found.Reason)
	assert.Equal(t, "user-1", found.RaisedBy)
}

func TestDisputeRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Reason", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, dispute))

	// Resolve the dispute
	require.NoError(t, dispute.Resolve("Fixed", "admin"))
	err = repo.Update(ctx, dispute)
	require.NoError(t, err)

	// Verify update persisted
	found, err := repo.FindByID(ctx, dispute.DisputeID)
	require.NoError(t, err)
	assert.Equal(t, domain.DisputeStatusResolved, found.Status)
	assert.Equal(t, "Fixed", found.Resolution)
	assert.Equal(t, "admin", found.ResolvedBy)
	assert.NotNil(t, found.ResolvedAt)
}

func TestDisputeRepository_FindByVarianceID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	// Create two disputes for the same variance
	d1, err := domain.NewDispute(varianceID, runID, "ACC-001", "First dispute", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d1))

	d2, err := domain.NewDispute(varianceID, runID, "ACC-001", "Second dispute", "user-2")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d2))

	found, err := repo.FindByVarianceID(ctx, varianceID)
	require.NoError(t, err)
	assert.Len(t, found, 2)
}

func TestDisputeRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	snapshotID := uuid.New()
	varianceID := uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	d1, err := domain.NewDispute(varianceID, runID, "ACC-001", "Reason 1", "user-1")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d1))

	d2, err := domain.NewDispute(varianceID, runID, "ACC-002", "Reason 2", "user-2")
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, d2))

	t.Run("list all", func(t *testing.T) {
		found, err := repo.List(ctx, domain.DisputeFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("filter by account", func(t *testing.T) {
		accountID := "ACC-001"
		found, err := repo.List(ctx, domain.DisputeFilter{AccountID: &accountID})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "ACC-001", found[0].AccountID)
	})

	t.Run("filter by status", func(t *testing.T) {
		s := domain.DisputeStatusOpen
		found, err := repo.List(ctx, domain.DisputeFilter{Status: &s})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})
}

func TestDisputeRepository_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
