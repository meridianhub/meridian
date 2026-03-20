package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence/testhelpers"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionRepository_InsertWithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	t.Run("insert within transaction and commit", func(t *testing.T) {
		tx, err := tc.PositionRepo.BeginTx(ctx)
		require.NoError(t, err)

		pos, err := domain.NewPosition(
			"ACC-TX-001",
			"GBP",
			"default",
			decimal.NewFromFloat(250.00),
			"Monetary",
			map[string]string{"via": "tx"},
			uuid.New(),
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.InsertWithTx(ctx, tx, pos)
		require.NoError(t, err)

		err = tx.Commit(ctx)
		require.NoError(t, err)

		// Verify via FindByID
		found, err := tc.PositionRepo.FindByID(ctx, pos.ID)
		require.NoError(t, err)
		assert.Equal(t, pos.ID, found.ID)
		assert.True(t, decimal.NewFromFloat(250.00).Equal(found.Amount))
		assert.Equal(t, "tx", found.Attributes["via"])
	})

	t.Run("insert within transaction and rollback", func(t *testing.T) {
		tx, err := tc.PositionRepo.BeginTx(ctx)
		require.NoError(t, err)

		pos, err := domain.NewPosition(
			"ACC-TX-002",
			"USD",
			"default",
			decimal.NewFromFloat(100.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.InsertWithTx(ctx, tx, pos)
		require.NoError(t, err)

		// Rollback instead of commit
		err = tx.Rollback(ctx)
		require.NoError(t, err)

		// Should NOT be found
		_, err = tc.PositionRepo.FindByID(ctx, pos.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("nil position returns error", func(t *testing.T) {
		tx, err := tc.PositionRepo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		err = tc.PositionRepo.InsertWithTx(ctx, tx, nil)
		assert.ErrorIs(t, err, persistence.ErrNilPosition)
	})

	t.Run("duplicate ID within tx returns conflict", func(t *testing.T) {
		tx, err := tc.PositionRepo.BeginTx(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()

		pos, err := domain.NewPosition(
			"ACC-TX-DUP",
			"EUR",
			"default",
			decimal.NewFromFloat(50.00),
			"Monetary",
			nil,
			uuid.Nil,
			"system",
		)
		require.NoError(t, err)

		err = tc.PositionRepo.InsertWithTx(ctx, tx, pos)
		require.NoError(t, err)

		// Insert same position again
		err = tc.PositionRepo.InsertWithTx(ctx, tx, pos)
		assert.ErrorIs(t, err, domain.ErrConflict)
	})
}

func TestPositionRepository_BeginTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tc := testhelpers.SetupTestContainer(t)
	defer tc.Cleanup(t)

	ctx := context.Background()

	tx, err := tc.PositionRepo.BeginTx(ctx)
	require.NoError(t, err)
	require.NotNil(t, tx)
	_ = tx.Rollback(ctx)
}
