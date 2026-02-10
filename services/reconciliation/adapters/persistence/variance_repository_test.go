package persistence_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedRunAndSnapshot creates prerequisite settlement_run and snapshot records,
// returning the surrogate IDs needed for variance FK references.
func seedRunAndSnapshot(ctx context.Context, t *testing.T, db *gorm.DB, runBusinessID, snapshotBusinessID uuid.UUID) (runSurrogateID, snapshotSurrogateID uuid.UUID) {
	t.Helper()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())

	// Insert settlement_run and capture surrogate ID
	err := db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_run" (run_id, account_id, scope, settlement_type, period_start, period_end, initiated_by) VALUES (?, 'ACC-001', 'ACCOUNT', 'DAILY', NOW() - INTERVAL '1 day', NOW(), 'system')`, quoted),
		runBusinessID,
	).Error
	require.NoError(t, err)

	var runIDStr string
	err = db.WithContext(ctx).Raw(
		fmt.Sprintf(`SELECT id::text FROM %s."settlement_run" WHERE run_id = ?`, quoted),
		runBusinessID,
	).Scan(&runIDStr).Error
	require.NoError(t, err)
	runSurrogateID, err = uuid.Parse(runIDStr)
	require.NoError(t, err)

	// Insert settlement_snapshot and capture surrogate ID
	err = db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_snapshot" (snapshot_id, run_id, account_id, instrument_code, expected_balance, actual_balance, variance_amount, source_system, captured_at) VALUES (?, ?, 'ACC-001', 'GBP', 100.00, 90.00, -10.00, 'test', NOW())`, quoted),
		snapshotBusinessID, runSurrogateID,
	).Error
	require.NoError(t, err)

	var snapIDStr string
	err = db.WithContext(ctx).Raw(
		fmt.Sprintf(`SELECT id::text FROM %s."settlement_snapshot" WHERE snapshot_id = ?`, quoted),
		snapshotBusinessID,
	).Scan(&snapIDStr).Error
	require.NoError(t, err)
	snapshotSurrogateID, err = uuid.Parse(snapIDStr)
	require.NoError(t, err)

	return runSurrogateID, snapshotSurrogateID
}

func newTestVariance(t *testing.T, runSurrogateID, snapshotSurrogateID uuid.UUID) *domain.Variance {
	t.Helper()
	v, err := domain.NewVariance(
		runSurrogateID,
		snapshotSurrogateID,
		"ACC-001",
		"GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(90.00),
		domain.VarianceReasonAmountMismatch,
	)
	require.NoError(t, err)
	return v
}

func TestVarianceRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	v := newTestVariance(t, runID, snapshotID)
	v.Attributes = map[string]string{"source": "test"}

	err := repo.Create(ctx, v)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, v.VarianceID)
	require.NoError(t, err)
	assert.Equal(t, v.VarianceID, found.VarianceID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, "GBP", found.InstrumentCode)
	assert.True(t, decimal.NewFromFloat(100.00).Equal(found.ExpectedAmount))
	assert.True(t, decimal.NewFromFloat(90.00).Equal(found.ActualAmount))
	assert.True(t, decimal.NewFromFloat(-10.00).Equal(found.VarianceAmount))
	assert.Equal(t, domain.VarianceStatusDetected, found.Status)
	assert.Equal(t, domain.VarianceReasonAmountMismatch, found.Reason)
	assert.Equal(t, "test", found.Attributes["source"])
	assert.Empty(t, found.ResolutionNote)
	assert.Empty(t, found.ResolvedBy)
	assert.Nil(t, found.ResolvedAt)
}

func TestVarianceRepository_CreateBatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	variances := make([]*domain.Variance, 150)
	for i := range variances {
		v := newTestVariance(t, runID, snapshotID)
		v.AccountID = fmt.Sprintf("ACC-%03d", i)
		variances[i] = v
	}

	err := repo.CreateBatch(ctx, variances)
	require.NoError(t, err)

	// Verify all 150 were persisted
	found, err := repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	assert.Len(t, found, 150)

	t.Run("empty batch is no-op", func(t *testing.T) {
		err := repo.CreateBatch(ctx, []*domain.Variance{})
		require.NoError(t, err)
	})
}

func TestVarianceRepository_FindByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	t.Run("existing variance with nullable fields", func(t *testing.T) {
		runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())
		v := newTestVariance(t, runID, snapshotID)
		require.NoError(t, repo.Create(ctx, v))

		// Resolve to populate nullable fields
		require.NoError(t, v.Resolve("Fixed the mismatch", "admin"))
		require.NoError(t, repo.Update(ctx, v))

		found, err := repo.FindByID(ctx, v.VarianceID)
		require.NoError(t, err)
		assert.Equal(t, v.VarianceID, found.VarianceID)
		assert.Equal(t, "Fixed the mismatch", found.ResolutionNote)
		assert.Equal(t, "admin", found.ResolvedBy)
		assert.NotNil(t, found.ResolvedAt)
		assert.Equal(t, domain.VarianceStatusResolved, found.Status)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestVarianceRepository_FindByRunID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	// Create two runs with variances
	run1ID, snap1ID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())
	run2ID, snap2ID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	for i := 0; i < 3; i++ {
		v := newTestVariance(t, run1ID, snap1ID)
		v.AccountID = fmt.Sprintf("ACC-%03d", i)
		require.NoError(t, repo.Create(ctx, v))
	}
	for i := 0; i < 2; i++ {
		v := newTestVariance(t, run2ID, snap2ID)
		v.AccountID = fmt.Sprintf("ACC-R2-%03d", i)
		require.NoError(t, repo.Create(ctx, v))
	}

	t.Run("run1 has 3 variances", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, run1ID)
		require.NoError(t, err)
		assert.Len(t, found, 3)
	})

	t.Run("run2 has 2 variances", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, run2ID)
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("nonexistent run returns empty", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, uuid.New())
		require.NoError(t, err)
		assert.Empty(t, found)
	})
}

func TestVarianceRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())
	v := newTestVariance(t, runID, snapshotID)
	v.Attributes = map[string]string{"initial": "true"}
	require.NoError(t, repo.Create(ctx, v))

	t.Run("update status and resolution", func(t *testing.T) {
		require.NoError(t, v.Resolve("Corrected via adjustment", "operator-1"))
		v.Attributes["resolved"] = "true"
		err := repo.Update(ctx, v)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, v.VarianceID)
		require.NoError(t, err)
		assert.Equal(t, domain.VarianceStatusResolved, found.Status)
		assert.Equal(t, "Corrected via adjustment", found.ResolutionNote)
		assert.Equal(t, "operator-1", found.ResolvedBy)
		assert.NotNil(t, found.ResolvedAt)
		assert.Equal(t, "true", found.Attributes["resolved"])
		assert.Equal(t, "true", found.Attributes["initial"])
	})

	t.Run("update value delta and currency", func(t *testing.T) {
		runID2, snapshotID2 := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())
		v2 := newTestVariance(t, runID2, snapshotID2)
		require.NoError(t, repo.Create(ctx, v2))

		require.NoError(t, v2.Value(decimal.NewFromFloat(150.50), "GBP"))
		err := repo.Update(ctx, v2)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, v2.VarianceID)
		require.NoError(t, err)
		assert.Equal(t, domain.VarianceStatusValued, found.Status)
		assert.True(t, decimal.NewFromFloat(150.50).Equal(found.ValueDelta))
		assert.Equal(t, "GBP", found.Currency)
	})

	t.Run("update not found", func(t *testing.T) {
		phantom := newTestVariance(t, runID, snapshotID)
		phantom.VarianceID = uuid.New()
		err := repo.Update(ctx, phantom)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestVarianceRepository_DeleteByRunID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	run1ID, snap1ID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())
	run2ID, snap2ID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	// Create variances for both runs
	for i := 0; i < 3; i++ {
		v := newTestVariance(t, run1ID, snap1ID)
		v.AccountID = fmt.Sprintf("ACC-D1-%03d", i)
		require.NoError(t, repo.Create(ctx, v))
	}
	for i := 0; i < 2; i++ {
		v := newTestVariance(t, run2ID, snap2ID)
		v.AccountID = fmt.Sprintf("ACC-D2-%03d", i)
		require.NoError(t, repo.Create(ctx, v))
	}

	// Delete run1's variances
	err := repo.DeleteByRunID(ctx, run1ID)
	require.NoError(t, err)

	// run1 should have no variances
	found, err := repo.FindByRunID(ctx, run1ID)
	require.NoError(t, err)
	assert.Empty(t, found)

	// run2 should still have its variances
	found, err = repo.FindByRunID(ctx, run2ID)
	require.NoError(t, err)
	assert.Len(t, found, 2)

	t.Run("idempotent delete on empty", func(t *testing.T) {
		err := repo.DeleteByRunID(ctx, run1ID)
		require.NoError(t, err)
	})

	t.Run("delete nonexistent run is no-op", func(t *testing.T) {
		err := repo.DeleteByRunID(ctx, uuid.New())
		require.NoError(t, err)
	})
}

func TestVarianceRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	// Create variances with different attributes
	v1 := newTestVariance(t, runID, snapshotID)
	v1.AccountID = "ACC-L001"
	require.NoError(t, repo.Create(ctx, v1))

	v2, err := domain.NewVariance(
		runID, snapshotID, "ACC-L002", "EUR",
		decimal.NewFromFloat(200.00), decimal.NewFromFloat(195.00),
		domain.VarianceReasonTimingDifference,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, v2))

	// Resolve v2 to give it a different status
	require.NoError(t, v2.Resolve("Timing corrected", "admin"))
	require.NoError(t, repo.Update(ctx, v2))

	v3 := newTestVariance(t, runID, snapshotID)
	v3.AccountID = "ACC-L001"
	require.NoError(t, repo.Create(ctx, v3))

	t.Run("list all", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 3)
	})

	t.Run("filter by run_id", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{RunID: &runID})
		require.NoError(t, err)
		assert.Len(t, found, 3)
	})

	t.Run("filter by account_id", func(t *testing.T) {
		accountID := "ACC-L001"
		found, err := repo.List(ctx, domain.VarianceFilter{AccountID: &accountID})
		require.NoError(t, err)
		assert.Len(t, found, 2)
		for _, f := range found {
			assert.Equal(t, "ACC-L001", f.AccountID)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		status := domain.VarianceStatusDetected
		found, err := repo.List(ctx, domain.VarianceFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("filter by reason", func(t *testing.T) {
		reason := domain.VarianceReasonTimingDifference
		found, err := repo.List(ctx, domain.VarianceFilter{Reason: &reason})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "ACC-L002", found[0].AccountID)
	})

	t.Run("filter by resolved status", func(t *testing.T) {
		status := domain.VarianceStatusResolved
		found, err := repo.List(ctx, domain.VarianceFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "ACC-L002", found[0].AccountID)
	})

	t.Run("combined filters", func(t *testing.T) {
		accountID := "ACC-L001"
		reason := domain.VarianceReasonAmountMismatch
		found, err := repo.List(ctx, domain.VarianceFilter{
			AccountID: &accountID,
			Reason:    &reason,
		})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})
}

func TestVarianceRepository_ListPagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewVarianceRepository(db)
	ctx := tenantCtx()

	runID, snapshotID := seedRunAndSnapshot(ctx, t, db, uuid.New(), uuid.New())

	// Create 500 variances in batches for efficiency
	const total = 500
	variances := make([]*domain.Variance, total)
	for i := range variances {
		v := newTestVariance(t, runID, snapshotID)
		v.AccountID = fmt.Sprintf("ACC-P%04d", i)
		// Stagger created_at slightly so ordering is deterministic
		v.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		variances[i] = v
	}
	require.NoError(t, repo.CreateBatch(ctx, variances))

	t.Run("default limit returns 50", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 50)
	})

	t.Run("custom limit", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{Limit: 10})
		require.NoError(t, err)
		assert.Len(t, found, 10)
	})

	t.Run("offset", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{Limit: 100, Offset: 450})
		require.NoError(t, err)
		assert.Len(t, found, 50)
	})

	t.Run("max limit capped at 1000", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{Limit: 5000})
		require.NoError(t, err)
		assert.Len(t, found, 500) // Only 500 exist, but limit is capped to 1000
	})

	t.Run("offset beyond dataset returns empty", func(t *testing.T) {
		found, err := repo.List(ctx, domain.VarianceFilter{Limit: 50, Offset: 600})
		require.NoError(t, err)
		assert.Empty(t, found)
	})

	t.Run("pages are disjoint", func(t *testing.T) {
		page1, err := repo.List(ctx, domain.VarianceFilter{Limit: 100, Offset: 0})
		require.NoError(t, err)
		page2, err := repo.List(ctx, domain.VarianceFilter{Limit: 100, Offset: 100})
		require.NoError(t, err)

		assert.Len(t, page1, 100)
		assert.Len(t, page2, 100)

		// Verify no overlap
		page1IDs := make(map[uuid.UUID]bool)
		for _, v := range page1 {
			page1IDs[v.VarianceID] = true
		}
		for _, v := range page2 {
			assert.False(t, page1IDs[v.VarianceID], "pages should not overlap")
		}
	})
}
