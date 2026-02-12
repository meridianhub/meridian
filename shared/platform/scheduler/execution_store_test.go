package scheduler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

// setupTestCockroachDB creates a CockroachDB testcontainer with the scheduler_execution table.
func setupTestCockroachDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("Failed to start CockroachDB container: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = crdbContainer.Terminate(cleanupCtx)
	})

	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection config: %v", err)
	}

	pool, err := pgxpool.New(ctx, connConfig.ConnString())
	if err != nil {
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	// Create the scheduler_execution table
	createTableSQL := `
		CREATE TABLE scheduler_execution (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			scheduler_name  VARCHAR(100) NOT NULL,
			schedule_id     VARCHAR(200) NOT NULL,
			scheduled_at    TIMESTAMPTZ NOT NULL,
			executed_at     TIMESTAMPTZ,
			completed_at    TIMESTAMPTZ,
			status          VARCHAR(20) NOT NULL DEFAULT 'TRIGGERED',
			result_ref      VARCHAR(200),
			error_message   TEXT,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT chk_scheduler_execution_status
				CHECK (status IN ('TRIGGERED', 'COMPLETED', 'FAILED', 'MISSED', 'SKIPPED'))
		);
		CREATE INDEX idx_scheduler_execution_scheduler_schedule ON scheduler_execution (scheduler_name, schedule_id, scheduled_at DESC);
	`
	_, err = pool.Exec(ctx, createTableSQL)
	if err != nil {
		t.Fatalf("Failed to create scheduler_execution table: %v", err)
	}

	return pool
}

func TestIntegration_PgExecutionStore_RecordAndRetrieve(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	execID := uuid.New()
	schedulerName := "test-scheduler"
	scheduleID := "sched-" + uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Microsecond)

	exec := scheduler.Execution{
		ID:            execID,
		SchedulerName: schedulerName,
		ScheduleID:    scheduleID,
		ScheduledAt:   now,
		ExecutedAt:    &now,
		Status:        scheduler.ExecutionStatusTriggered,
	}

	err := store.RecordExecution(ctx, exec)
	require.NoError(t, err)

	last, err := store.LastExecution(ctx, schedulerName, scheduleID)
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, execID, last.ID)
	assert.Equal(t, schedulerName, last.SchedulerName)
	assert.Equal(t, scheduleID, last.ScheduleID)
	assert.Equal(t, scheduler.ExecutionStatusTriggered, last.Status)
}

func TestIntegration_PgExecutionStore_UpdateToCompleted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	execID := uuid.New()
	schedulerName := "test-scheduler"
	scheduleID := "sched-" + uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Microsecond)

	exec := scheduler.Execution{
		ID:            execID,
		SchedulerName: schedulerName,
		ScheduleID:    scheduleID,
		ScheduledAt:   now,
		ExecutedAt:    &now,
		Status:        scheduler.ExecutionStatusTriggered,
	}

	err := store.RecordExecution(ctx, exec)
	require.NoError(t, err)

	resultRef := "run-123"
	err = store.UpdateExecution(ctx, execID, scheduler.ExecutionStatusCompleted, &resultRef, nil)
	require.NoError(t, err)

	last, err := store.LastExecution(ctx, schedulerName, scheduleID)
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, scheduler.ExecutionStatusCompleted, last.Status)
	assert.NotNil(t, last.CompletedAt)
	assert.NotNil(t, last.ResultRef)
	assert.Equal(t, resultRef, *last.ResultRef)
}

func TestIntegration_PgExecutionStore_UpdateToFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	execID := uuid.New()
	schedulerName := "test-scheduler"
	scheduleID := "sched-" + uuid.NewString()[:8]
	now := time.Now().UTC().Truncate(time.Microsecond)

	exec := scheduler.Execution{
		ID:            execID,
		SchedulerName: schedulerName,
		ScheduleID:    scheduleID,
		ScheduledAt:   now,
		ExecutedAt:    &now,
		Status:        scheduler.ExecutionStatusTriggered,
	}

	err := store.RecordExecution(ctx, exec)
	require.NoError(t, err)

	errMsg := "gRPC unavailable"
	err = store.UpdateExecution(ctx, execID, scheduler.ExecutionStatusFailed, nil, &errMsg)
	require.NoError(t, err)

	last, err := store.LastExecution(ctx, schedulerName, scheduleID)
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, scheduler.ExecutionStatusFailed, last.Status)
	assert.NotNil(t, last.ErrorMessage)
	assert.Equal(t, errMsg, *last.ErrorMessage)
}

func TestIntegration_PgExecutionStore_LastExecution_ReturnsLatest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	schedulerName := "test-scheduler"
	scheduleID := "sched-" + uuid.NewString()[:8]

	older := scheduler.Execution{
		ID:            uuid.New(),
		SchedulerName: schedulerName,
		ScheduleID:    scheduleID,
		ScheduledAt:   time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond),
		Status:        scheduler.ExecutionStatusCompleted,
	}
	err := store.RecordExecution(ctx, older)
	require.NoError(t, err)

	recent := scheduler.Execution{
		ID:            uuid.New(),
		SchedulerName: schedulerName,
		ScheduleID:    scheduleID,
		ScheduledAt:   time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Microsecond),
		Status:        scheduler.ExecutionStatusCompleted,
	}
	err = store.RecordExecution(ctx, recent)
	require.NoError(t, err)

	last, err := store.LastExecution(ctx, schedulerName, scheduleID)
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, recent.ID, last.ID)
}

func TestIntegration_PgExecutionStore_LastExecution_NoRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	last, err := store.LastExecution(ctx, "nonexistent", "nonexistent")
	assert.ErrorIs(t, err, scheduler.ErrNoExecution)
	assert.Nil(t, last)
}

func TestIntegration_PgExecutionStore_IsolatesBySchedulerAndScheduleID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)

	// Insert for scheduler-A, sched-1
	execA := scheduler.Execution{
		ID:            uuid.New(),
		SchedulerName: "scheduler-A",
		ScheduleID:    "sched-1",
		ScheduledAt:   now,
		Status:        scheduler.ExecutionStatusCompleted,
	}
	err := store.RecordExecution(ctx, execA)
	require.NoError(t, err)

	// Insert for scheduler-B, sched-1
	execB := scheduler.Execution{
		ID:            uuid.New(),
		SchedulerName: "scheduler-B",
		ScheduleID:    "sched-1",
		ScheduledAt:   now,
		Status:        scheduler.ExecutionStatusFailed,
	}
	err = store.RecordExecution(ctx, execB)
	require.NoError(t, err)

	// Query scheduler-A should return execA
	lastA, err := store.LastExecution(ctx, "scheduler-A", "sched-1")
	require.NoError(t, err)
	assert.Equal(t, execA.ID, lastA.ID)
	assert.Equal(t, scheduler.ExecutionStatusCompleted, lastA.Status)

	// Query scheduler-B should return execB
	lastB, err := store.LastExecution(ctx, "scheduler-B", "sched-1")
	require.NoError(t, err)
	assert.Equal(t, execB.ID, lastB.ID)
	assert.Equal(t, scheduler.ExecutionStatusFailed, lastB.Status)
}

func TestIntegration_PgExecutionStore_UpdateNonExistentRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	err := store.UpdateExecution(ctx, uuid.New(), scheduler.ExecutionStatusCompleted, nil, nil)
	assert.ErrorIs(t, err, scheduler.ErrExecutionNotFound)
}

func TestIntegration_PgExecutionStore_StatusConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	ctx := context.Background()

	// Try to insert an invalid status directly
	_, err := pool.Exec(ctx, `
		INSERT INTO scheduler_execution (id, scheduler_name, schedule_id, scheduled_at, status)
		VALUES ($1, 'test', 'sched-1', now(), 'INVALID_STATUS')`,
		uuid.New())
	require.Error(t, err, "invalid status should be rejected by check constraint")
	assert.Contains(t, err.Error(), "CHECK constraint")
}

func TestIntegration_PgExecutionStore_AllValidStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := setupTestCockroachDB(t)
	store := scheduler.NewPgExecutionStore(pool)
	ctx := context.Background()

	statuses := []scheduler.ExecutionStatus{
		scheduler.ExecutionStatusTriggered,
		scheduler.ExecutionStatusCompleted,
		scheduler.ExecutionStatusFailed,
		scheduler.ExecutionStatusMissed,
		scheduler.ExecutionStatusSkipped,
	}

	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			exec := scheduler.Execution{
				ID:            uuid.New(),
				SchedulerName: "test-scheduler",
				ScheduleID:    fmt.Sprintf("sched-%s", status),
				ScheduledAt:   time.Now().UTC().Truncate(time.Microsecond),
				Status:        status,
			}
			err := store.RecordExecution(ctx, exec)
			require.NoError(t, err)

			last, err := store.LastExecution(ctx, "test-scheduler", exec.ScheduleID)
			require.NoError(t, err)
			assert.Equal(t, status, last.Status)
		})
	}
}
