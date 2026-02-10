package persistence_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSettlementRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		time.Now().UTC().Add(-24*time.Hour),
		time.Now().UTC(),
		"system",
	)
	require.NoError(t, err)
	return run
}

func TestSettlementRunRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	run.Attributes = map[string]string{"source": "test"}

	err := repo.Create(ctx, run)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, run.RunID, found.RunID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, domain.ReconciliationScopeAccount, found.Scope)
	assert.Equal(t, domain.SettlementTypeDaily, found.SettlementType)
	assert.Equal(t, domain.RunStatusPending, found.Status)
	assert.Equal(t, "system", found.InitiatedBy)
	assert.Equal(t, int64(1), found.Version)
	assert.Equal(t, "test", found.Attributes["source"])
	assert.Nil(t, found.CompletedAt)
	assert.Equal(t, 0, found.VarianceCount)
	assert.Empty(t, found.FailureReason)
}

func TestSettlementRunRepository_FindByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	t.Run("existing run", func(t *testing.T) {
		run := newTestSettlementRun(t)
		require.NoError(t, repo.Create(ctx, run))

		found, err := repo.FindByID(ctx, run.RunID)
		require.NoError(t, err)
		assert.Equal(t, run.RunID, found.RunID)
		assert.Equal(t, run.AccountID, found.AccountID)
		assert.WithinDuration(t, run.PeriodStart, found.PeriodStart, time.Second)
		assert.WithinDuration(t, run.PeriodEnd, found.PeriodEnd, time.Second)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestSettlementRunRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	require.NoError(t, repo.Create(ctx, run))

	// Transition to RUNNING
	require.NoError(t, run.Start())
	err := repo.Update(ctx, run)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusRunning, found.Status)
	assert.Equal(t, int64(2), found.Version)

	// Complete the run
	require.NoError(t, run.Complete(5))
	err = repo.Update(ctx, run)
	require.NoError(t, err)

	found, err = repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusCompleted, found.Status)
	assert.Equal(t, 5, found.VarianceCount)
	assert.NotNil(t, found.CompletedAt)
	assert.Equal(t, int64(3), found.Version)
}

func TestSettlementRunRepository_UpdateOptimisticLock(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	require.NoError(t, repo.Create(ctx, run))

	// Load two copies to simulate concurrent access
	copy1, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)

	copy2, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, copy1.Start())
	err = repo.Update(ctx, copy1)
	require.NoError(t, err)

	// Second update with stale version fails
	require.NoError(t, copy2.Start())
	err = repo.Update(ctx, copy2)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)
}

func TestSettlementRunRepository_UpdateNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	// Bump version so the WHERE version = version-1 check doesn't match version 0
	run.Version = 2

	err := repo.Update(ctx, run)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSettlementRunRepository_UpdateFailedRun(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	require.NoError(t, repo.Create(ctx, run))

	require.NoError(t, run.Start())
	require.NoError(t, repo.Update(ctx, run))

	require.NoError(t, run.Fail("database timeout"))
	require.NoError(t, repo.Update(ctx, run))

	found, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFailed, found.Status)
	assert.Equal(t, "database timeout", found.FailureReason)
	assert.NotNil(t, found.CompletedAt)
}

func TestSettlementRunRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	now := time.Now().UTC()

	// Create runs with different attributes
	run1 := newTestSettlementRun(t)
	run1.AccountID = "ACC-001"
	run1.Scope = domain.ReconciliationScopeAccount
	require.NoError(t, repo.Create(ctx, run1))

	run2, err := domain.NewSettlementRun(
		"ACC-002",
		domain.ReconciliationScopePortfolio,
		domain.SettlementTypeWeekly,
		now.Add(-7*24*time.Hour),
		now,
		"admin",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, run2))

	run3, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeMonthly,
		now.Add(-30*24*time.Hour),
		now,
		"system",
	)
	require.NoError(t, err)
	// Start and complete run3 so it has a different status
	require.NoError(t, run3.Start())
	require.NoError(t, repo.Create(ctx, run3))

	t.Run("list all", func(t *testing.T) {
		found, err := repo.List(ctx, domain.RunFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 3)
	})

	t.Run("filter by account", func(t *testing.T) {
		accountID := "ACC-001"
		found, err := repo.List(ctx, domain.RunFilter{AccountID: &accountID})
		require.NoError(t, err)
		assert.Len(t, found, 2)
		for _, r := range found {
			assert.Equal(t, "ACC-001", r.AccountID)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		status := domain.RunStatusPending
		found, err := repo.List(ctx, domain.RunFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("filter by scope", func(t *testing.T) {
		scope := domain.ReconciliationScopePortfolio
		found, err := repo.List(ctx, domain.RunFilter{Scope: &scope})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "ACC-002", found[0].AccountID)
	})

	t.Run("filter by running status", func(t *testing.T) {
		status := domain.RunStatusRunning
		found, err := repo.List(ctx, domain.RunFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, run3.RunID, found[0].RunID)
	})
}

func TestSettlementRunRepository_ListPagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	// Create 5 runs
	for i := 0; i < 5; i++ {
		run := newTestSettlementRun(t)
		require.NoError(t, repo.Create(ctx, run))
	}

	t.Run("limit", func(t *testing.T) {
		found, err := repo.List(ctx, domain.RunFilter{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("offset", func(t *testing.T) {
		found, err := repo.List(ctx, domain.RunFilter{Limit: 10, Offset: 3})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("default limit", func(t *testing.T) {
		found, err := repo.List(ctx, domain.RunFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 5)
	})

	t.Run("max limit cap", func(t *testing.T) {
		found, err := repo.List(ctx, domain.RunFilter{Limit: 5000})
		require.NoError(t, err)
		assert.Len(t, found, 5)
	})
}
