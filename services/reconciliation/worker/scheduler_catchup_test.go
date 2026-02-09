package worker

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- In-memory ExecutionStore mock ---

type inMemoryExecutionStore struct {
	mu         sync.Mutex
	executions []SchedulerExecution
}

func newInMemoryExecutionStore() *inMemoryExecutionStore {
	return &inMemoryExecutionStore{}
}

func (s *inMemoryExecutionStore) RecordExecution(_ context.Context, exec SchedulerExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = append(s.executions, exec)
	return nil
}

func (s *inMemoryExecutionStore) UpdateExecution(_ context.Context, id uuid.UUID, status ExecutionStatus, runID *uuid.UUID, errMsg *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.executions {
		if e.ID == id {
			s.executions[i].Status = status
			s.executions[i].RunID = runID
			s.executions[i].ErrorMessage = errMsg
			now := time.Now().UTC()
			s.executions[i].ExecutedAt = &now
			return nil
		}
	}
	return nil
}

func (s *inMemoryExecutionStore) LastExecution(_ context.Context, scheduleName string) (*SchedulerExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *SchedulerExecution
	for i, e := range s.executions {
		if e.ScheduleName == scheduleName {
			if latest == nil || e.ScheduledAt.After(latest.ScheduledAt) {
				latest = &s.executions[i]
			}
		}
	}
	if latest == nil {
		return nil, ErrNoExecution
	}
	return latest, nil
}

func (s *inMemoryExecutionStore) getExecutions() []SchedulerExecution {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]SchedulerExecution, len(s.executions))
	copy(result, s.executions)
	return result
}

// --- Tests ---

func TestSettlementScheduler_AuditTrail_RecordsExecution(t *testing.T) {
	store := newInMemoryExecutionStore()
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "* * * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
		},
	}
	recon := &mockReconClient{runID: uuid.New().String()}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, defaultConfig(),
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)

	// Execute a job
	scheduler.executeJob(refData.schedules[0])

	// Verify audit trail
	execs := store.getExecutions()
	require.Len(t, execs, 1, "should have 1 execution record")
	assert.Equal(t, "sched-1", execs[0].ScheduleName)
	assert.Equal(t, ExecutionStatusCompleted, execs[0].Status)
	assert.NotNil(t, execs[0].RunID, "should have run ID set")
}

func TestSettlementScheduler_AuditTrail_RecordsFailure(t *testing.T) {
	store := newInMemoryExecutionStore()
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "* * * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
		},
	}
	recon := &mockReconClient{err: assert.AnError}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, defaultConfig(),
		testLogger(), metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)

	scheduler.executeJob(refData.schedules[0])

	execs := store.getExecutions()
	require.Len(t, execs, 1)
	assert.Equal(t, ExecutionStatusFailed, execs[0].Status)
	assert.NotNil(t, execs[0].ErrorMessage)
}

func TestSettlementScheduler_AuditTrail_RecordsSkippedForDuplicate(t *testing.T) {
	store := newInMemoryExecutionStore()
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "* * * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
		},
	}
	recon := &mockReconClient{err: ErrRunAlreadyExists}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, defaultConfig(),
		testLogger(), metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)

	scheduler.executeJob(refData.schedules[0])

	execs := store.getExecutions()
	require.Len(t, execs, 1)
	assert.Equal(t, ExecutionStatusSkipped, execs[0].Status, "duplicate run should be SKIPPED not FAILED")
}

func TestSettlementScheduler_CatchUp_TriggersMissedWindows(t *testing.T) {
	// Fix the time so catch-up calculations are deterministic
	fixedNow := time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC)
	origNow := NowFunc
	NowFunc = func() time.Time { return fixedNow }
	defer func() { NowFunc = origNow }()

	store := newInMemoryExecutionStore()

	// Simulate a schedule that fires daily at 2 AM UTC.
	// Last execution was 2 days ago, so yesterday's 2 AM was missed.
	twoDaysAgo := fixedNow.Add(-48 * time.Hour)
	store.RecordExecution(context.Background(), SchedulerExecution{
		ID:           uuid.New(),
		ScheduleName: "sched-1",
		ScheduledAt:  twoDaysAgo,
		Status:       ExecutionStatusCompleted,
	})

	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "0 2 * * *", // 2 AM daily
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
			},
		},
	}
	recon := &mockReconClient{runID: uuid.New().String()}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	cfg := defaultConfig()
	cfg.MaxCatchUpAge = 72 * time.Hour

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, cfg,
		testLogger(), metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	// Give time for startup + catch-up
	time.Sleep(200 * time.Millisecond)

	// Should have triggered catch-up runs.
	// Between 48h ago and now, at 2 AM daily: yesterday 2AM and today 2AM
	// Yesterday: 2026-02-08 02:00 UTC
	// Today: 2026-02-09 02:00 UTC (before fixedNow 14:00)
	requests := recon.getRequests()
	assert.GreaterOrEqual(t, len(requests), 1, "should have triggered at least 1 catch-up run")

	// Verify the catch-up runs are from the scheduler catch-up
	for _, req := range requests {
		assert.Equal(t, "acc-1", req.AccountID)
		assert.Equal(t, "settlement-scheduler-catchup", req.InitiatedBy,
			"catch-up runs should be marked with catchup initiator")
	}

	cancel()
	<-errCh
}

func TestSettlementScheduler_CatchUp_NoPriorExecution(t *testing.T) {
	// When there's no prior execution, catch-up uses MaxCatchUpAge as lookback
	fixedNow := time.Date(2026, 2, 9, 14, 0, 0, 0, time.UTC)
	origNow := NowFunc
	NowFunc = func() time.Time { return fixedNow }
	defer func() { NowFunc = origNow }()

	store := newInMemoryExecutionStore()
	// No prior executions

	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "0 2 * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
			},
		},
	}
	recon := &mockReconClient{runID: uuid.New().String()}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	cfg := defaultConfig()
	cfg.MaxCatchUpAge = 48 * time.Hour // Look back 2 days

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, cfg,
		testLogger(), metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// Should catch up: 2026-02-08 02:00 and 2026-02-09 02:00 (both within 48h lookback)
	requests := recon.getRequests()
	assert.GreaterOrEqual(t, len(requests), 1, "should catch up even with no prior execution")

	cancel()
	<-errCh
}

func TestSettlementScheduler_CatchUp_SkippedWhenNoStore(t *testing.T) {
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "0 2 * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
			},
		},
	}
	recon := &mockReconClient{runID: uuid.New().String()}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	// No WithExecutionStore - catch-up should be skipped
	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, defaultConfig(),
		testLogger(), metrics,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// No catch-up should have been triggered (cron fires are in the future)
	requests := recon.getRequests()
	assert.Empty(t, requests, "should not trigger catch-up without execution store")

	cancel()
	<-errCh
}

func TestSettlementScheduler_WithExecutionStore_Option(t *testing.T) {
	store := newInMemoryExecutionStore()
	refData := &mockRefDataClient{}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := NewSchedulerMetricsWithRegistry(prometheus.NewRegistry())

	scheduler, err := NewSettlementScheduler(
		refData, recon, leader, defaultConfig(),
		testLogger(), metrics,
		WithExecutionStore(store),
	)
	require.NoError(t, err)
	assert.NotNil(t, scheduler.executionStore, "execution store should be set via option")
}
