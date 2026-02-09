package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mocks ---

type mockRefDataClient struct {
	mu        sync.Mutex
	schedules []SettlementSchedule
	err       error
	callCount int
}

func (m *mockRefDataClient) ListSettlementSchedules(_ context.Context) ([]SettlementSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	result := make([]SettlementSchedule, len(m.schedules))
	copy(result, m.schedules)
	return result, nil
}

func (m *mockRefDataClient) setSchedules(s []SettlementSchedule) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedules = s
}

func (m *mockRefDataClient) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

type mockReconClient struct {
	mu        sync.Mutex
	requests  []InitiateRequest
	runID     string
	err       error
	callCount int32
}

func (m *mockReconClient) InitiateReconciliation(_ context.Context, req InitiateRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	atomic.AddInt32(&m.callCount, 1)
	m.requests = append(m.requests, req)
	if m.err != nil {
		return "", m.err
	}
	return m.runID, nil
}

func (m *mockReconClient) getRequests() []InitiateRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]InitiateRequest, len(m.requests))
	copy(result, m.requests)
	return result
}

type mockLeaderElector struct {
	mu         sync.Mutex
	leader     bool
	acquireErr error
}

func (m *mockLeaderElector) TryAcquire(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acquireErr != nil {
		return false, m.acquireErr
	}
	return m.leader, nil
}

func (m *mockLeaderElector) Release(_ context.Context) error {
	return nil
}

func (m *mockLeaderElector) IsLeader() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leader
}

// --- Helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func testMetrics(t *testing.T) *SchedulerMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewSchedulerMetricsWithRegistry(reg)
}

func defaultConfig() SchedulerConfig {
	return SchedulerConfig{
		PollInterval:    1 * time.Hour,
		ShutdownTimeout: 5 * time.Second,
	}
}

// --- Tests ---

func TestNewSettlementScheduler_ValidatesInputs(t *testing.T) {
	logger := testLogger()
	refData := &mockRefDataClient{}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	cfg := defaultConfig()
	metrics := testMetrics(t)

	tests := []struct {
		name    string
		setup   func() (*SettlementScheduler, error)
		wantErr error
	}{
		{
			name: "nil ref data client",
			setup: func() (*SettlementScheduler, error) {
				return NewSettlementScheduler(nil, recon, leader, cfg, logger, metrics)
			},
			wantErr: ErrNilRefDataClient,
		},
		{
			name: "nil recon client",
			setup: func() (*SettlementScheduler, error) {
				return NewSettlementScheduler(refData, nil, leader, cfg, logger, metrics)
			},
			wantErr: ErrNilReconClient,
		},
		{
			name: "nil leader elector",
			setup: func() (*SettlementScheduler, error) {
				return NewSettlementScheduler(refData, recon, nil, cfg, logger, metrics)
			},
			wantErr: ErrNilLeaderElector,
		},
		{
			name: "nil logger",
			setup: func() (*SettlementScheduler, error) {
				return NewSettlementScheduler(refData, recon, leader, cfg, nil, metrics)
			},
			wantErr: ErrNilLogger,
		},
		{
			name: "invalid poll interval",
			setup: func() (*SettlementScheduler, error) {
				badCfg := cfg
				badCfg.PollInterval = 0
				return NewSettlementScheduler(refData, recon, leader, badCfg, logger, metrics)
			},
			wantErr: ErrInvalidPollInterval,
		},
		{
			name: "invalid shutdown timeout",
			setup: func() (*SettlementScheduler, error) {
				badCfg := cfg
				badCfg.ShutdownTimeout = 0
				return NewSettlementScheduler(refData, recon, leader, badCfg, logger, metrics)
			},
			wantErr: ErrInvalidShutdownTimeout,
		},
		{
			name: "valid configuration",
			setup: func() (*SettlementScheduler, error) {
				return NewSettlementScheduler(refData, recon, leader, cfg, logger, metrics)
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler, err := tt.setup()
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, scheduler)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, scheduler)
			}
		})
	}
}

func TestSettlementScheduler_StartAndStop(t *testing.T) {
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "0 2 * * *",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
		},
	}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	// Give scheduler time to start and load schedules
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 1, scheduler.ScheduleCount(), "should have 1 registered schedule")

	// Stop via context cancellation
	cancel()

	err = <-errCh
	assert.NoError(t, err)
}

func TestSettlementScheduler_DoubleStartReturnsError(t *testing.T) {
	refData := &mockRefDataClient{}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Second start should return error
	err = scheduler.Start(ctx)
	assert.ErrorIs(t, err, ErrAlreadyRunning)

	cancel()
	<-errCh
}

func TestSettlementScheduler_StopGracefully(t *testing.T) {
	refData := &mockRefDataClient{}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Stop via Stop()
	scheduler.Stop()

	err = <-errCh
	assert.NoError(t, err)
}

func TestSettlementScheduler_RefreshSchedules(t *testing.T) {
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
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	cfg := defaultConfig()
	cfg.PollInterval = 100 * time.Millisecond

	scheduler, err := NewSettlementScheduler(refData, recon, leader, cfg, testLogger(), metrics)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, scheduler.ScheduleCount())

	// Add a second schedule
	refData.setSchedules([]SettlementSchedule{
		{
			ScheduleID:     "sched-1",
			AssetType:      "GBP",
			AccountID:      "acc-1",
			CronExpression: "0 2 * * *",
			SettlementType: "DAILY",
			Scope:          "ACCOUNT",
		},
		{
			ScheduleID:     "sched-2",
			AssetType:      "kWh",
			AccountID:      "acc-2",
			CronExpression: "30 3 * * *",
			SettlementType: "DAILY",
			Scope:          "INSTRUMENT",
		},
	})

	// Wait for refresh
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 2, scheduler.ScheduleCount(), "should have 2 schedules after refresh")

	// Remove a schedule
	refData.setSchedules([]SettlementSchedule{
		{
			ScheduleID:     "sched-2",
			AssetType:      "kWh",
			AccountID:      "acc-2",
			CronExpression: "30 3 * * *",
			SettlementType: "DAILY",
			Scope:          "INSTRUMENT",
		},
	})

	// Wait for refresh
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, scheduler.ScheduleCount(), "should have 1 schedule after removal")

	cancel()
	<-errCh
}

func TestSettlementScheduler_InvalidCronExpression(t *testing.T) {
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "bad-sched",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "not a cron expression",
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
			},
		},
	}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Invalid cron should be skipped, not crash
	assert.Equal(t, 0, scheduler.ScheduleCount(), "invalid cron should not be registered")

	cancel()
	<-errCh
}

func TestSettlementScheduler_ExecuteJobAsLeader(t *testing.T) {
	refData := &mockRefDataClient{
		schedules: []SettlementSchedule{
			{
				ScheduleID:     "sched-1",
				AssetType:      "GBP",
				AccountID:      "acc-1",
				CronExpression: "* * * * *", // Every minute (for test speed)
				SettlementType: "DAILY",
				Scope:          "ACCOUNT",
				PeriodOffset:   24 * time.Hour,
			},
		},
	}
	recon := &mockReconClient{runID: "run-123"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	// Directly call executeJob to test the execution path
	sched := refData.schedules[0]
	scheduler.executeJob(sched)

	requests := recon.getRequests()
	require.Len(t, requests, 1)
	assert.Equal(t, "acc-1", requests[0].AccountID)
	assert.Equal(t, "DAILY", requests[0].SettlementType)
	assert.Equal(t, "ACCOUNT", requests[0].Scope)
	assert.Equal(t, "settlement-scheduler", requests[0].InitiatedBy)
	assert.True(t, requests[0].PeriodStart.Before(requests[0].PeriodEnd))
}

func TestSettlementScheduler_SkipsJobWhenNotLeader(t *testing.T) {
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
	recon := &mockReconClient{runID: "run-123"}
	leader := &mockLeaderElector{leader: false} // Not the leader
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	sched := refData.schedules[0]
	scheduler.executeJob(sched)

	requests := recon.getRequests()
	assert.Empty(t, requests, "should not initiate reconciliation when not leader")
}

func TestSettlementScheduler_LeaderElectionFailure(t *testing.T) {
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
	recon := &mockReconClient{runID: "run-123"}
	leader := &mockLeaderElector{acquireErr: errors.New("redis connection error")}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	sched := refData.schedules[0]
	scheduler.executeJob(sched)

	requests := recon.getRequests()
	assert.Empty(t, requests, "should not initiate reconciliation on leader election error")
}

func TestSettlementScheduler_ReconciliationError(t *testing.T) {
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
	recon := &mockReconClient{err: errors.New("gRPC unavailable")}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	// Should not panic on error
	sched := refData.schedules[0]
	scheduler.executeJob(sched)

	requests := recon.getRequests()
	assert.Len(t, requests, 1, "should still call reconciliation client")
}

func TestSettlementScheduler_RefreshError(t *testing.T) {
	refData := &mockRefDataClient{
		err: errors.New("reference data unavailable"),
	}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	cfg := defaultConfig()
	cfg.PollInterval = 50 * time.Millisecond

	scheduler, err := NewSettlementScheduler(refData, recon, leader, cfg, testLogger(), metrics)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- scheduler.Start(ctx)
	}()

	// Should start and continue running despite refresh errors
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 0, scheduler.ScheduleCount(), "should have no schedules on error")
	assert.True(t, refData.getCallCount() >= 2, "should have retried")

	cancel()
	<-errCh
}

func TestSettlementScheduler_SkipsJobWhenStopped(t *testing.T) {
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
	recon := &mockReconClient{runID: "run-123"}
	leader := &mockLeaderElector{leader: true}
	metrics := testMetrics(t)

	scheduler, err := NewSettlementScheduler(refData, recon, leader, defaultConfig(), testLogger(), metrics)
	require.NoError(t, err)

	// Mark as stopped before executing
	scheduler.mu.Lock()
	scheduler.stopped = true
	scheduler.mu.Unlock()

	sched := refData.schedules[0]
	scheduler.executeJob(sched)

	requests := recon.getRequests()
	assert.Empty(t, requests, "should not execute when stopped")
}

func TestSettlementScheduler_NilMetricsUsesDefault(t *testing.T) {
	refData := &mockRefDataClient{}
	recon := &mockReconClient{runID: "run-1"}
	leader := &mockLeaderElector{leader: true}
	cfg := defaultConfig()

	// Should not panic with nil metrics
	scheduler, err := NewSettlementScheduler(refData, recon, leader, cfg, testLogger(), nil)
	require.NoError(t, err)
	assert.NotNil(t, scheduler)
}
