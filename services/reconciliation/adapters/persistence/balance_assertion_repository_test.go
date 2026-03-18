package persistence_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBalanceAssertion(t *testing.T) *domain.BalanceAssertion {
	t.Helper()
	runID := uuid.New()
	a, err := domain.NewBalanceAssertion(
		&runID, "ACC-001", "GBP",
		"sum(positions) == expected",
		decimal.NewFromFloat(10000.00),
	)
	require.NoError(t, err)
	return a
}

func TestBalanceAssertionRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	assertion.Attributes = map[string]string{"source": "test"}
	assertion.Metadata = map[string]string{"tenant": "demo"}

	err := repo.Create(ctx, assertion)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, assertion.AssertionID, found.AssertionID)
	assert.Equal(t, "ACC-001", found.AccountID)
	assert.Equal(t, "GBP", found.InstrumentCode)
	assert.Equal(t, "sum(positions) == expected", found.Expression)
	assert.True(t, decimal.NewFromFloat(10000.00).Equal(found.ExpectedBalance))
	assert.True(t, decimal.Zero.Equal(found.ActualBalance))
	assert.Equal(t, domain.AssertionStatusPending, found.Status)
	assert.Equal(t, int64(1), found.Version)
	assert.Equal(t, "test", found.Attributes["source"])
	assert.Equal(t, "demo", found.Metadata["tenant"])
	assert.Empty(t, found.FailureReason)
	assert.Empty(t, found.OverrideReason)
	assert.NotNil(t, found.RunID)
}

func TestBalanceAssertionRepository_CreateStandalone(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	a, err := domain.NewBalanceAssertion(
		nil, "ACC-002", "EUR",
		"total_debits == total_credits",
		decimal.Zero,
	)
	require.NoError(t, err)

	err = repo.Create(ctx, a)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, a.AssertionID)
	require.NoError(t, err)
	assert.Nil(t, found.RunID)
	assert.Equal(t, "ACC-002", found.AccountID)
	assert.Equal(t, "EUR", found.InstrumentCode)
}

func TestBalanceAssertionRepository_FindByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	t.Run("existing assertion", func(t *testing.T) {
		assertion := newTestBalanceAssertion(t)
		require.NoError(t, repo.Create(ctx, assertion))

		found, err := repo.FindByID(ctx, assertion.AssertionID)
		require.NoError(t, err)
		assert.Equal(t, assertion.AssertionID, found.AssertionID)
		assert.Equal(t, assertion.AccountID, found.AccountID)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestBalanceAssertionRepository_FindByRunID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()

	a1, err := domain.NewBalanceAssertion(&runID, "ACC-001", "GBP", "expr1", decimal.NewFromFloat(100.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a1))

	a2, err := domain.NewBalanceAssertion(&runID, "ACC-001", "EUR", "expr2", decimal.NewFromFloat(200.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a2))

	// Different run
	otherRunID := uuid.New()
	a3, err := domain.NewBalanceAssertion(&otherRunID, "ACC-001", "GBP", "expr3", decimal.NewFromFloat(300.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a3))

	found, err := repo.FindByRunID(ctx, runID)
	require.NoError(t, err)
	assert.Len(t, found, 2)

	t.Run("empty result for unknown run", func(t *testing.T) {
		found, err := repo.FindByRunID(ctx, uuid.New())
		require.NoError(t, err)
		assert.Empty(t, found)
	})
}

func TestBalanceAssertionRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	require.NoError(t, repo.Create(ctx, assertion))

	// Pass the assertion
	require.NoError(t, assertion.Pass(decimal.NewFromFloat(10000.00)))
	err := repo.Update(ctx, assertion)
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, found.Status)
	assert.True(t, decimal.NewFromFloat(10000.00).Equal(found.ActualBalance))
	assert.Equal(t, int64(2), found.Version)
	assert.False(t, found.AssertedAt.IsZero())
}

func TestBalanceAssertionRepository_UpdateFailed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	require.NoError(t, repo.Create(ctx, assertion))

	require.NoError(t, assertion.Fail(decimal.NewFromFloat(9500.00), "Balance mismatch"))
	require.NoError(t, repo.Update(ctx, assertion))

	found, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, found.Status)
	assert.True(t, decimal.NewFromFloat(9500.00).Equal(found.ActualBalance))
	assert.Equal(t, "Balance mismatch", found.FailureReason)
	assert.Equal(t, int64(2), found.Version)
}

func TestBalanceAssertionRepository_UpdateOverride(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	require.NoError(t, repo.Create(ctx, assertion))

	// Fail then override
	require.NoError(t, assertion.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
	require.NoError(t, repo.Update(ctx, assertion))

	require.NoError(t, assertion.Override("approved by manager"))
	require.NoError(t, repo.Update(ctx, assertion))

	found, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusOverride, found.Status)
	assert.Equal(t, "mismatch", found.FailureReason)
	assert.Equal(t, "approved by manager", found.OverrideReason)
	assert.Equal(t, int64(3), found.Version)
}

func TestBalanceAssertionRepository_UpdateOptimisticLock(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	require.NoError(t, repo.Create(ctx, assertion))

	// Load two copies
	copy1, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)

	copy2, err := repo.FindByID(ctx, assertion.AssertionID)
	require.NoError(t, err)

	// First update succeeds
	require.NoError(t, copy1.Pass(decimal.NewFromFloat(10000.00)))
	err = repo.Update(ctx, copy1)
	require.NoError(t, err)

	// Second update with stale version fails
	require.NoError(t, copy2.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
	err = repo.Update(ctx, copy2)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)
}

func TestBalanceAssertionRepository_UpdateNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	assertion := newTestBalanceAssertion(t)
	assertion.Version = 2

	err := repo.Update(ctx, assertion)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestBalanceAssertionRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	otherRunID := uuid.New()

	a1, err := domain.NewBalanceAssertion(&runID, "ACC-001", "GBP", "expr1", decimal.NewFromFloat(100.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a1))

	a2, err := domain.NewBalanceAssertion(&runID, "ACC-002", "EUR", "expr2", decimal.NewFromFloat(200.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a2))

	a3, err := domain.NewBalanceAssertion(&otherRunID, "ACC-001", "GBP", "expr3", decimal.NewFromFloat(300.00))
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, a3))

	// Fail one for status filtering
	require.NoError(t, a3.Fail(decimal.NewFromFloat(250.00), "mismatch"))
	require.NoError(t, repo.Update(ctx, a3))

	t.Run("list all", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 3)
	})

	t.Run("filter by run", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{RunID: &runID})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("filter by account", func(t *testing.T) {
		accountID := "ACC-001"
		found, err := repo.List(ctx, domain.AssertionFilter{AccountID: &accountID})
		require.NoError(t, err)
		assert.Len(t, found, 2)
		for _, a := range found {
			assert.Equal(t, "ACC-001", a.AccountID)
		}
	})

	t.Run("filter by instrument code", func(t *testing.T) {
		instrumentCode := "EUR"
		found, err := repo.List(ctx, domain.AssertionFilter{InstrumentCode: &instrumentCode})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, "EUR", found[0].InstrumentCode)
	})

	t.Run("filter by status", func(t *testing.T) {
		status := domain.AssertionStatusFailed
		found, err := repo.List(ctx, domain.AssertionFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 1)
		assert.Equal(t, domain.AssertionStatusFailed, found[0].Status)
	})

	t.Run("filter by pending status", func(t *testing.T) {
		status := domain.AssertionStatusPending
		found, err := repo.List(ctx, domain.AssertionFilter{Status: &status})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})
}

func TestBalanceAssertionRepository_ListPagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	// Create 5 assertions
	for i := 0; i < 5; i++ {
		a := newTestBalanceAssertion(t)
		require.NoError(t, repo.Create(ctx, a))
		// Small delay to ensure distinct created_at for ordering
		time.Sleep(time.Millisecond) //nolint:forbidigo // ensures distinct timestamps for DB ordering test
	}

	t.Run("limit", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("offset", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{Limit: 10, Offset: 3})
		require.NoError(t, err)
		assert.Len(t, found, 2)
	})

	t.Run("default limit", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{})
		require.NoError(t, err)
		assert.Len(t, found, 5)
	})

	t.Run("max limit cap", func(t *testing.T) {
		found, err := repo.List(ctx, domain.AssertionFilter{Limit: 5000})
		require.NoError(t, err)
		assert.Len(t, found, 5)
	})
}

func TestBalanceAssertionRepository_DomainConversionRoundtrip(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	runID := uuid.New()
	original, err := domain.NewBalanceAssertion(
		&runID, "ACC-001", "GBP",
		"sum(positions) == expected",
		decimal.NewFromFloat(10000.50),
	)
	require.NoError(t, err)
	original.Attributes = map[string]string{"key1": "val1", "key2": "val2"}
	original.Metadata = map[string]string{"env": "test", "region": "uk"}

	require.NoError(t, repo.Create(ctx, original))

	// Fail it to populate more fields
	require.NoError(t, original.Fail(decimal.NewFromFloat(9999.25), "off by 1.25"))
	require.NoError(t, repo.Update(ctx, original))

	// Override it
	require.NoError(t, original.Override("within tolerance"))
	require.NoError(t, repo.Update(ctx, original))

	found, err := repo.FindByID(ctx, original.AssertionID)
	require.NoError(t, err)

	assert.Equal(t, original.AssertionID, found.AssertionID)
	assert.Equal(t, *original.RunID, *found.RunID)
	assert.Equal(t, original.AccountID, found.AccountID)
	assert.Equal(t, original.InstrumentCode, found.InstrumentCode)
	assert.Equal(t, original.Expression, found.Expression)
	assert.True(t, original.ExpectedBalance.Equal(found.ExpectedBalance))
	assert.True(t, original.ActualBalance.Equal(found.ActualBalance))
	assert.Equal(t, original.Status, found.Status)
	assert.Equal(t, original.FailureReason, found.FailureReason)
	assert.Equal(t, original.OverrideReason, found.OverrideReason)
	assert.Equal(t, original.Attributes, found.Attributes)
	assert.Equal(t, original.Metadata, found.Metadata)
	assert.Equal(t, original.Version, found.Version)
	assert.False(t, found.AssertedAt.IsZero())
}

func TestBalanceAssertionRepository_JSONBSerialization(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewBalanceAssertionRepository(db)
	ctx := tenantCtx()

	t.Run("nil attributes and metadata", func(t *testing.T) {
		a := newTestBalanceAssertion(t)
		a.Attributes = nil
		a.Metadata = nil
		require.NoError(t, repo.Create(ctx, a))

		found, err := repo.FindByID(ctx, a.AssertionID)
		require.NoError(t, err)
		assert.Nil(t, found.Attributes)
		assert.Nil(t, found.Metadata)
	})

	t.Run("empty attributes and metadata", func(t *testing.T) {
		a := newTestBalanceAssertion(t)
		a.Attributes = map[string]string{}
		a.Metadata = map[string]string{}
		require.NoError(t, repo.Create(ctx, a))

		found, err := repo.FindByID(ctx, a.AssertionID)
		require.NoError(t, err)
		// Empty maps may come back as nil from JSONB
		assert.True(t, len(found.Attributes) == 0)
		assert.True(t, len(found.Metadata) == 0)
	})

	t.Run("populated attributes and metadata", func(t *testing.T) {
		a := newTestBalanceAssertion(t)
		a.Attributes = map[string]string{"key": "value", "special": "chars!@#$%"}
		a.Metadata = map[string]string{"version": "1.0", "source": "api"}
		require.NoError(t, repo.Create(ctx, a))

		found, err := repo.FindByID(ctx, a.AssertionID)
		require.NoError(t, err)
		assert.Equal(t, "value", found.Attributes["key"])
		assert.Equal(t, "chars!@#$%", found.Attributes["special"])
		assert.Equal(t, "1.0", found.Metadata["version"])
		assert.Equal(t, "api", found.Metadata["source"])
	})
}
