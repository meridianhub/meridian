package persistence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostgresRepository_CreateWithOutbox(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("creates log and calls postFn", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		postFnCalled := false

		err := tc.repo.CreateWithOutbox(ctx, log, func(_ pgx.Tx) error {
			postFnCalled = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, postFnCalled)

		// Verify the log was persisted
		retrieved, err := tc.repo.FindByID(ctx, log.LogID)
		require.NoError(t, err)
		assert.Equal(t, log.LogID, retrieved.LogID)
	})

	t.Run("nil postFn succeeds", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.CreateWithOutbox(ctx, log, nil)
		require.NoError(t, err)

		retrieved, err := tc.repo.FindByID(ctx, log.LogID)
		require.NoError(t, err)
		assert.Equal(t, log.LogID, retrieved.LogID)
	})

	t.Run("nil log returns error", func(t *testing.T) {
		err := tc.repo.CreateWithOutbox(ctx, nil, nil)
		assert.ErrorIs(t, err, persistence.ErrNilLog)
	})

	t.Run("duplicate log returns conflict", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.CreateWithOutbox(ctx, log, nil)
		require.NoError(t, err)

		err = tc.repo.CreateWithOutbox(ctx, log, nil)
		assert.ErrorIs(t, err, domain.ErrConflict)
	})

	t.Run("postFn error rolls back log creation", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		expectedErr := fmt.Errorf("outbox write failed")

		err := tc.repo.CreateWithOutbox(ctx, log, func(_ pgx.Tx) error {
			return expectedErr
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outbox write failed")

		// Verify the log was NOT persisted
		_, err = tc.repo.FindByID(ctx, log.LogID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestPostgresRepository_UpdateWithOutbox(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	t.Run("updates log and calls postFn", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.Create(ctx, log)
		require.NoError(t, err)

		err = log.MarkPosted("Posted", nil)
		require.NoError(t, err)

		postFnCalled := false
		err = tc.repo.UpdateWithOutbox(ctx, log, func(_ pgx.Tx) error {
			postFnCalled = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, postFnCalled)

		retrieved, err := tc.repo.FindByID(ctx, log.LogID)
		require.NoError(t, err)
		assert.Equal(t, domain.TransactionStatusPosted, retrieved.StatusTracking.CurrentStatus)
	})

	t.Run("nil postFn succeeds", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.Create(ctx, log)
		require.NoError(t, err)

		err = log.MarkPosted("Posted", nil)
		require.NoError(t, err)

		err = tc.repo.UpdateWithOutbox(ctx, log, nil)
		require.NoError(t, err)
	})

	t.Run("nil log returns error", func(t *testing.T) {
		err := tc.repo.UpdateWithOutbox(ctx, nil, nil)
		assert.ErrorIs(t, err, persistence.ErrNilLog)
	})

	t.Run("not found log returns error", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.UpdateWithOutbox(ctx, log, nil)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("optimistic lock failure", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.Create(ctx, log)
		require.NoError(t, err)

		err = log.MarkPosted("Posted", nil)
		require.NoError(t, err)

		err = tc.repo.UpdateWithOutbox(ctx, log, nil)
		require.NoError(t, err)

		// Try to update with stale version
		log.Version = 1
		err = tc.repo.UpdateWithOutbox(ctx, log, nil)
		assert.ErrorIs(t, err, domain.ErrOptimisticLock)
	})

	t.Run("postFn error rolls back update", func(t *testing.T) {
		log := createTestLog(t, testAccountID)
		err := tc.repo.Create(ctx, log)
		require.NoError(t, err)

		err = log.MarkPosted("Posted", nil)
		require.NoError(t, err)

		err = tc.repo.UpdateWithOutbox(ctx, log, func(_ pgx.Tx) error {
			return fmt.Errorf("outbox error")
		})
		require.Error(t, err)

		// Verify the update was rolled back
		retrieved, err := tc.repo.FindByID(ctx, log.LogID)
		require.NoError(t, err)
		assert.Equal(t, domain.TransactionStatusPending, retrieved.StatusTracking.CurrentStatus)
	})
}
