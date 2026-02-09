package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/worker"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ExecutionStore_CockroachDB tests the execution store against a real CockroachDB.
func TestIntegration_ExecutionStore_CockroachDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("reconciliation"))
	store := worker.NewPgExecutionStore(pool)
	ctx := context.Background()

	t.Run("RecordAndRetrieveExecution", func(t *testing.T) {
		execID := uuid.New()
		scheduleName := "test-schedule-" + uuid.NewString()[:8]

		exec := worker.SchedulerExecution{
			ID:           execID,
			ScheduleName: scheduleName,
			ScheduledAt:  time.Now().UTC().Truncate(time.Microsecond),
			Status:       worker.ExecutionStatusTriggered,
		}

		err := store.RecordExecution(ctx, exec)
		require.NoError(t, err)

		// Retrieve it
		last, err := store.LastExecution(ctx, scheduleName)
		require.NoError(t, err)
		require.NotNil(t, last)
		assert.Equal(t, execID, last.ID)
		assert.Equal(t, scheduleName, last.ScheduleName)
		assert.Equal(t, worker.ExecutionStatusTriggered, last.Status)
	})

	t.Run("UpdateExecutionStatus", func(t *testing.T) {
		execID := uuid.New()
		scheduleName := "test-schedule-" + uuid.NewString()[:8]
		runID := uuid.New()

		exec := worker.SchedulerExecution{
			ID:           execID,
			ScheduleName: scheduleName,
			ScheduledAt:  time.Now().UTC().Truncate(time.Microsecond),
			Status:       worker.ExecutionStatusTriggered,
		}

		err := store.RecordExecution(ctx, exec)
		require.NoError(t, err)

		// Update to completed
		err = store.UpdateExecution(ctx, execID, worker.ExecutionStatusCompleted, &runID, nil)
		require.NoError(t, err)

		// Verify
		last, err := store.LastExecution(ctx, scheduleName)
		require.NoError(t, err)
		require.NotNil(t, last)
		assert.Equal(t, worker.ExecutionStatusCompleted, last.Status)
		assert.NotNil(t, last.RunID)
		assert.Equal(t, runID, *last.RunID)
		assert.NotNil(t, last.ExecutedAt)
	})

	t.Run("UpdateExecutionWithError", func(t *testing.T) {
		execID := uuid.New()
		scheduleName := "test-schedule-" + uuid.NewString()[:8]
		errMsg := "gRPC unavailable"

		exec := worker.SchedulerExecution{
			ID:           execID,
			ScheduleName: scheduleName,
			ScheduledAt:  time.Now().UTC().Truncate(time.Microsecond),
			Status:       worker.ExecutionStatusTriggered,
		}

		err := store.RecordExecution(ctx, exec)
		require.NoError(t, err)

		err = store.UpdateExecution(ctx, execID, worker.ExecutionStatusFailed, nil, &errMsg)
		require.NoError(t, err)

		last, err := store.LastExecution(ctx, scheduleName)
		require.NoError(t, err)
		require.NotNil(t, last)
		assert.Equal(t, worker.ExecutionStatusFailed, last.Status)
		assert.NotNil(t, last.ErrorMessage)
		assert.Equal(t, errMsg, *last.ErrorMessage)
	})

	t.Run("LastExecution_ReturnsLatest", func(t *testing.T) {
		scheduleName := "test-schedule-" + uuid.NewString()[:8]

		// Insert two executions
		old := worker.SchedulerExecution{
			ID:           uuid.New(),
			ScheduleName: scheduleName,
			ScheduledAt:  time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond),
			Status:       worker.ExecutionStatusCompleted,
		}
		err := store.RecordExecution(ctx, old)
		require.NoError(t, err)

		recent := worker.SchedulerExecution{
			ID:           uuid.New(),
			ScheduleName: scheduleName,
			ScheduledAt:  time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond),
			Status:       worker.ExecutionStatusCompleted,
		}
		err = store.RecordExecution(ctx, recent)
		require.NoError(t, err)

		// Should return the most recent
		last, err := store.LastExecution(ctx, scheduleName)
		require.NoError(t, err)
		require.NotNil(t, last)
		assert.Equal(t, recent.ID, last.ID)
	})

	t.Run("LastExecution_ReturnsErrNoExecution", func(t *testing.T) {
		last, err := store.LastExecution(ctx, "nonexistent-schedule")
		require.Error(t, err)
		assert.True(t, errors.Is(err, worker.ErrNoExecution))
		assert.Nil(t, last)
	})
}

// TestIntegration_SettlementRunUniqueConstraint_CockroachDB tests that the unique
// constraint on (account_id, period_start, period_end) prevents duplicate settlement runs.
func TestIntegration_SettlementRunUniqueConstraint_CockroachDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("reconciliation"))
	ctx := context.Background()

	accountID := "acc-test-" + uuid.NewString()[:8]
	periodStart := time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)

	// Insert first run
	_, err := pool.Exec(ctx, `
		INSERT INTO settlement_run (run_id, account_id, scope, settlement_type, status, period_start, period_end, initiated_by)
		VALUES ($1, $2, 'ACCOUNT', 'DAILY', 'PENDING', $3, $4, 'test')`,
		uuid.New(), accountID, periodStart, periodEnd)
	require.NoError(t, err)

	// Insert duplicate run with same account_id, period_start, period_end
	_, err = pool.Exec(ctx, `
		INSERT INTO settlement_run (run_id, account_id, scope, settlement_type, status, period_start, period_end, initiated_by)
		VALUES ($1, $2, 'ACCOUNT', 'DAILY', 'PENDING', $3, $4, 'test')`,
		uuid.New(), accountID, periodStart, periodEnd)
	require.Error(t, err, "duplicate settlement run should be rejected by unique constraint")
	assert.Contains(t, err.Error(), "idx_settlement_run_account_period",
		"error should reference the unique index")
}
