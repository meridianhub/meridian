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

// seedRunIDs holds the two distinct identifiers for a seeded settlement_run row.
type seedRunIDs struct {
	// SurrogateID is the settlement_run.id (DB surrogate PK); used as the FK target in settlement_snapshot.run_id.
	SurrogateID uuid.UUID
	// BusinessID is the settlement_run.run_id (business identifier); passed to MarkRunSnapshotsFinal and similar methods.
	BusinessID uuid.UUID
}

// seedSettlementRun creates a settlement_run record and returns the surrogate ID (settlement_run.id).
// For tests that also need the business run_id, use seedSettlementRunFull.
func seedSettlementRun(t *testing.T, ctx context.Context, db *gorm.DB) uuid.UUID {
	t.Helper()
	return seedSettlementRunFull(t, ctx, db).SurrogateID
}

// seedSettlementRunFull creates a settlement_run record and returns both the surrogate ID and the business run_id.
func seedSettlementRunFull(t *testing.T, ctx context.Context, db *gorm.DB) seedRunIDs {
	t.Helper()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())

	surrogateID := uuid.New()
	runID := uuid.New()

	err := db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s."settlement_run" (id, run_id, account_id, scope, settlement_type, period_start, period_end, initiated_by) VALUES (?, ?, 'ACC-001', 'ACCOUNT', 'DAILY', NOW() - INTERVAL '1 day', NOW(), 'system')`, quoted),
		surrogateID, runID,
	).Error
	require.NoError(t, err)
	return seedRunIDs{SurrogateID: surrogateID, BusinessID: runID}
}

func newTestSnapshot(t *testing.T, runID uuid.UUID) *domain.SettlementSnapshot {
	t.Helper()
	return &domain.SettlementSnapshot{
		SnapshotID:      uuid.New(),
		RunID:           runID,
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		ExpectedBalance: decimal.NewFromFloat(1000.123456789012345678),
		ActualBalance:   decimal.NewFromFloat(990.123456789012345678),
		VarianceAmount:  decimal.NewFromFloat(-10.0),
		SourceSystem:    "position-keeping",
		Attributes:      map[string]string{"bucket": "default"},
		CapturedAt:      time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
	}
}

func TestSettlementSnapshotRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	runID := seedSettlementRun(t, ctx, db)
	snapshot := newTestSnapshot(t, runID)

	err := repo.Create(ctx, snapshot)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, snapshot.SnapshotID, found.SnapshotID)
	assert.Equal(t, runID, found.RunID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, "GBP", found.InstrumentCode)
	assert.Equal(t, "position-keeping", found.SourceSystem)
	assert.Equal(t, "default", found.Attributes["bucket"])
	assert.WithinDuration(t, snapshot.CapturedAt, found.CapturedAt, time.Second)
}

func TestSettlementSnapshotRepository_CreateBatch(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	runID := seedSettlementRun(t, ctx, db)

	// Create 250 snapshots to test batching (batch size 100)
	snapshots := make([]*domain.SettlementSnapshot, 250)
	for i := range snapshots {
		snapshots[i] = newTestSnapshot(t, runID)
		snapshots[i].AccountID = fmt.Sprintf("ACC-%03d", i)
	}

	err := repo.CreateBatch(ctx, snapshots)
	require.NoError(t, err)

	found, err := repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	assert.Len(t, found, 250)
}

func TestSettlementSnapshotRepository_CreateBatchEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	err := repo.CreateBatch(ctx, nil)
	require.NoError(t, err)

	err = repo.CreateBatch(ctx, []*domain.SettlementSnapshot{})
	require.NoError(t, err)
}

func TestSettlementSnapshotRepository_FindByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	t.Run("existing snapshot with decimal precision", func(t *testing.T) {
		runID := seedSettlementRun(t, ctx, db)
		snapshot := newTestSnapshot(t, runID)
		snapshot.ExpectedBalance = decimal.RequireFromString("12345678901234567890.123456789012345678")
		snapshot.ActualBalance = decimal.RequireFromString("12345678901234567890.123456789012345670")
		snapshot.VarianceAmount = snapshot.ActualBalance.Sub(snapshot.ExpectedBalance)
		require.NoError(t, repo.Create(ctx, snapshot))

		found, err := repo.FindByID(ctx, snapshot.SnapshotID)
		require.NoError(t, err)
		assert.True(t, snapshot.ExpectedBalance.Equal(found.ExpectedBalance),
			"expected %s, got %s", snapshot.ExpectedBalance, found.ExpectedBalance)
		assert.True(t, snapshot.ActualBalance.Equal(found.ActualBalance),
			"expected %s, got %s", snapshot.ActualBalance, found.ActualBalance)
		assert.True(t, snapshot.VarianceAmount.Equal(found.VarianceAmount),
			"expected %s, got %s", snapshot.VarianceAmount, found.VarianceAmount)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestSettlementSnapshotRepository_FindByRunID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	runID1 := seedSettlementRun(t, ctx, db)
	runID2 := seedSettlementRun(t, ctx, db)

	// Create 3 snapshots for run1
	for i := 0; i < 3; i++ {
		s := newTestSnapshot(t, runID1)
		s.AccountID = fmt.Sprintf("ACC-%03d", i)
		require.NoError(t, repo.Create(ctx, s))
	}

	// Create 2 snapshots for run2
	for i := 0; i < 2; i++ {
		s := newTestSnapshot(t, runID2)
		s.AccountID = fmt.Sprintf("ACC-%03d", i+10)
		require.NoError(t, repo.Create(ctx, s))
	}

	t.Run("filter by run1", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, runID1)
		require.NoError(t, err)
		assert.Len(t, found, 3)
		for _, s := range found {
			assert.Equal(t, runID1, s.RunID)
		}
	})

	t.Run("filter by run2", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, runID2)
		require.NoError(t, err)
		assert.Len(t, found, 2)
		for _, s := range found {
			assert.Equal(t, runID2, s.RunID)
		}
	})

	t.Run("no results for unknown run", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, uuid.New())
		require.NoError(t, err)
		assert.Empty(t, found)
	})
}

func TestSettlementSnapshotRepository_DeleteByRunID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	runID := seedSettlementRun(t, ctx, db)

	// Create snapshots
	for i := 0; i < 5; i++ {
		s := newTestSnapshot(t, runID)
		s.AccountID = fmt.Sprintf("ACC-%03d", i)
		require.NoError(t, repo.Create(ctx, s))
	}

	// Verify they exist
	found, err := repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	assert.Len(t, found, 5)

	// Delete by run ID
	err = repo.DeleteByRunID(ctx, runID)
	require.NoError(t, err)

	// Verify cleanup
	found, err = repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	assert.Empty(t, found)

	// Idempotent: deleting again is a no-op
	err = repo.DeleteByRunID(ctx, runID)
	require.NoError(t, err)
}

func TestSettlementSnapshotRepository_MarkRunSnapshotsFinal(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	// Use seedSettlementRunFull so we have both IDs:
	//   SurrogateID → stored in settlement_snapshot.run_id (FK target)
	//   BusinessID  → passed to MarkRunSnapshotsFinal (business identifier)
	run := seedSettlementRunFull(t, ctx, db)

	// Create snapshots with the surrogate ID so the FK constraint is satisfied.
	s1 := newTestSnapshot(t, run.SurrogateID)
	s1.Attributes = map[string]string{"bucket": "default"}
	require.NoError(t, repo.Create(ctx, s1))

	s2 := newTestSnapshot(t, run.SurrogateID)
	s2.AccountID = "ACC-002"
	s2.Attributes = nil // Test nil attributes case
	require.NoError(t, repo.Create(ctx, s2))

	s3 := newTestSnapshot(t, run.SurrogateID)
	s3.AccountID = "ACC-003"
	s3.Attributes = map[string]string{"region": "eu-west-1"}
	require.NoError(t, repo.Create(ctx, s3))

	// Mark all snapshots as FINAL using the business run_id (not the surrogate PK).
	// MarkRunSnapshotsFinal resolves the business ID to the surrogate PK internally.
	err := repo.MarkRunSnapshotsFinal(ctx, run.BusinessID)
	require.NoError(t, err)

	// Verify all snapshots have settlement_type=FINAL
	found, err := repo.FindByRunID(ctx, run.SurrogateID)
	require.NoError(t, err)
	assert.Len(t, found, 3)
	for _, snap := range found {
		assert.Equal(t, "FINAL", snap.Attributes["settlement_type"],
			"snapshot %s missing settlement_type=FINAL", snap.SnapshotID)
	}

	// Verify existing attributes are preserved
	f1, err := repo.FindByID(ctx, s1.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, "default", f1.Attributes["bucket"])
	assert.Equal(t, "FINAL", f1.Attributes["settlement_type"])

	f3, err := repo.FindByID(ctx, s3.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", f3.Attributes["region"])
	assert.Equal(t, "FINAL", f3.Attributes["settlement_type"])
}

func TestSettlementSnapshotRepository_CreateBatchRollback(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementSnapshotRepository(db)
	ctx := tenantCtx()

	runID := seedSettlementRun(t, ctx, db)

	// Create a valid snapshot first
	existing := newTestSnapshot(t, runID)
	require.NoError(t, repo.Create(ctx, existing))

	// Create a batch where one snapshot has a duplicate snapshot_id (constraint violation)
	snapshots := make([]*domain.SettlementSnapshot, 3)
	snapshots[0] = newTestSnapshot(t, runID)
	snapshots[0].AccountID = "ACC-BATCH-1"
	snapshots[1] = newTestSnapshot(t, runID)
	snapshots[1].AccountID = "ACC-BATCH-2"
	snapshots[1].SnapshotID = existing.SnapshotID // Duplicate - will cause constraint violation
	snapshots[2] = newTestSnapshot(t, runID)
	snapshots[2].AccountID = "ACC-BATCH-3"

	err := repo.CreateBatch(ctx, snapshots)
	require.Error(t, err)

	// Verify none of the batch was persisted (atomic rollback)
	found, err := repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	// Only the original existing snapshot should remain
	assert.Len(t, found, 1)
	assert.Equal(t, existing.SnapshotID, found[0].SnapshotID)
}
