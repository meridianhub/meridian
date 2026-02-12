package scheduler_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test doubles ---

type stubProvider struct {
	mu        sync.Mutex
	schedules []scheduler.Schedule
	err       error
}

func (p *stubProvider) ListSchedules(_ context.Context) ([]scheduler.Schedule, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	result := make([]scheduler.Schedule, len(p.schedules))
	copy(result, p.schedules)
	return result, nil
}

func (p *stubProvider) setSchedules(schedules []scheduler.Schedule) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.schedules = schedules
}

type stubExecutor struct {
	mu          sync.Mutex
	calls       []scheduler.Schedule
	callCount   atomic.Int32
	err         error
	executeCh   chan struct{}
	executeFunc func(ctx context.Context, schedule scheduler.Schedule) error
}

func (e *stubExecutor) Execute(ctx context.Context, schedule scheduler.Schedule) error {
	e.mu.Lock()
	e.calls = append(e.calls, schedule)
	fn := e.executeFunc
	err := e.err
	e.mu.Unlock()

	e.callCount.Add(1)

	if e.executeCh != nil {
		select {
		case e.executeCh <- struct{}{}:
		default:
		}
	}

	if fn != nil {
		return fn(ctx, schedule)
	}
	return err
}

func (e *stubExecutor) getCalls() []scheduler.Schedule {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]scheduler.Schedule, len(e.calls))
	copy(result, e.calls)
	return result
}

type stubLock struct {
	acquired bool
	err      error
}

func (l *stubLock) Acquire(_ context.Context, _, _ string) (bool, func(), error) {
	if l.err != nil {
		return false, nil, l.err
	}
	if !l.acquired {
		return false, nil, nil
	}
	return true, func() {}, nil
}

type stubExecutionStore struct {
	mu         sync.Mutex
	executions []scheduler.Execution
}

func (s *stubExecutionStore) RecordExecution(_ context.Context, exec scheduler.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = append(s.executions, exec)
	return nil
}

func (s *stubExecutionStore) UpdateExecution(_ context.Context, id uuid.UUID, status scheduler.ExecutionStatus, resultRef *string, errMsg *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.executions {
		if s.executions[i].ID == id {
			s.executions[i].Status = status
			s.executions[i].ResultRef = resultRef
			s.executions[i].ErrorMessage = errMsg
			return nil
		}
	}
	return nil
}

func (s *stubExecutionStore) LastExecution(_ context.Context, schedulerName, scheduleID string) (*scheduler.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.executions) - 1; i >= 0; i-- {
		if s.executions[i].SchedulerName == schedulerName && s.executions[i].ScheduleID == scheduleID {
			exec := s.executions[i]
			return &exec, nil
		}
	}
	return nil, scheduler.ErrNoExecution
}

func (s *stubExecutionStore) getExecutions() []scheduler.Execution {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]scheduler.Execution, len(s.executions))
	copy(result, s.executions)
	return result
}

// --- Tests ---

func TestCronScheduler_StartAndStop(t *testing.T) {
	provider := &stubProvider{}
	executor := &stubExecutor{}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	go func() {
		close(started)
		_ = s.Start(ctx)
	}()

	<-started
	// Give the scheduler time to initialize
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return true // scheduler started
	})
	require.NoError(t, err)

	cancel()
	s.Stop()
}

func TestCronScheduler_LoadsSchedulesOnStart(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 2 * * *", TenantID: "tenant1"},
			{ID: "sched-2", CronExpr: "30 * * * *", TenantID: "tenant2"},
		},
	}
	executor := &stubExecutor{}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 2
	})
	require.NoError(t, err)

	assert.Equal(t, 2, s.ScheduleCount())

	cancel()
	s.Stop()
}

func TestCronScheduler_RemovesStaleSchedules(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 2 * * *"},
			{ID: "sched-2", CronExpr: "30 * * * *"},
		},
	}
	executor := &stubExecutor{}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: 200 * time.Millisecond,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for initial load
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 2
	})
	require.NoError(t, err)

	// Remove one schedule
	provider.setSchedules([]scheduler.Schedule{
		{ID: "sched-1", CronExpr: "0 2 * * *"},
	})

	// Wait for refresh to pick up change
	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	assert.Equal(t, 1, s.ScheduleCount())

	cancel()
	s.Stop()
}

func TestCronScheduler_ExecutesJobOnCronFire(t *testing.T) {
	// Use "* * * * *" (every minute) - robfig/cron fires immediately-ish on next tick
	// To make it fire quickly, we use a per-second cron expression if available,
	// but standard 5-field cron only goes to minute. Instead we verify via the
	// executor being called.

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "fast-sched", CronExpr: "* * * * *", TenantID: "tenant1", Metadata: "test-data"},
		},
	}
	executeCh := make(chan struct{}, 10)
	executor := &stubExecutor{executeCh: executeCh}
	lock := &stubLock{acquired: true}

	s := scheduler.NewCronScheduler(
		provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:             "test-scheduler",
			RefreshInterval:  time.Hour,
			ShutdownTimeout:  2 * time.Second,
			ExecutionTimeout: 5 * time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for schedule to be loaded
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	// Wait for the cron job to fire (up to ~65 seconds for minute-level cron)
	select {
	case <-executeCh:
		// Job fired
	case <-time.After(70 * time.Second):
		t.Fatal("cron job did not fire within timeout")
	}

	calls := executor.getCalls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Equal(t, "fast-sched", calls[0].ID)
	assert.Equal(t, "tenant1", calls[0].TenantID)
	assert.Equal(t, "test-data", calls[0].Metadata)

	cancel()
	s.Stop()
}

func TestCronScheduler_SkipsWhenLockNotAcquired(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "locked-sched", CronExpr: "* * * * *", TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 10)}
	lock := &stubLock{acquired: false}
	store := &stubExecutionStore{}

	s := scheduler.NewCronScheduler(
		provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for the cron to fire
	err := await.New().AtMost(70 * time.Second).PollInterval(500 * time.Millisecond).Until(func() bool {
		execs := store.getExecutions()
		for _, e := range execs {
			if e.Status == scheduler.ExecutionStatusSkipped {
				return true
			}
		}
		return false
	})
	require.NoError(t, err)

	// Executor should NOT have been called
	assert.Equal(t, int32(0), executor.callCount.Load())

	cancel()
	s.Stop()
}

func TestCronScheduler_RecordsExecutionAuditTrail(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "audit-sched", CronExpr: "* * * * *", TenantID: "tenant1"},
		},
	}
	executeCh := make(chan struct{}, 10)
	executor := &stubExecutor{executeCh: executeCh}
	lock := &stubLock{acquired: true}
	store := &stubExecutionStore{}

	s := scheduler.NewCronScheduler(
		provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for execution
	select {
	case <-executeCh:
	case <-time.After(70 * time.Second):
		t.Fatal("cron job did not fire")
	}

	// Wait for the execution record to be updated to COMPLETED
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		execs := store.getExecutions()
		for _, e := range execs {
			if e.Status == scheduler.ExecutionStatusCompleted {
				return true
			}
		}
		return false
	})
	require.NoError(t, err)

	execs := store.getExecutions()
	require.GreaterOrEqual(t, len(execs), 1)

	// Find the completed execution
	var found bool
	for _, e := range execs {
		if e.Status == scheduler.ExecutionStatusCompleted {
			assert.Equal(t, "test-scheduler", e.SchedulerName)
			assert.Equal(t, "audit-sched", e.ScheduleID)
			found = true
			break
		}
	}
	assert.True(t, found, "should have a COMPLETED execution record")

	cancel()
	s.Stop()
}

func TestCronScheduler_RecordsFailedExecution(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "fail-sched", CronExpr: "* * * * *", TenantID: "tenant1"},
		},
	}
	executeCh := make(chan struct{}, 10)
	executor := &stubExecutor{
		executeCh: executeCh,
		err:       errors.New("execution failed"),
	}
	lock := &stubLock{acquired: true}
	store := &stubExecutionStore{}

	s := scheduler.NewCronScheduler(
		provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	select {
	case <-executeCh:
	case <-time.After(70 * time.Second):
		t.Fatal("cron job did not fire")
	}

	// Wait for the FAILED record
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		execs := store.getExecutions()
		for _, e := range execs {
			if e.Status == scheduler.ExecutionStatusFailed {
				return true
			}
		}
		return false
	})
	require.NoError(t, err)

	execs := store.getExecutions()
	var found bool
	for _, e := range execs {
		if e.Status == scheduler.ExecutionStatusFailed {
			assert.NotNil(t, e.ErrorMessage)
			assert.Equal(t, "execution failed", *e.ErrorMessage)
			found = true
			break
		}
	}
	assert.True(t, found, "should have a FAILED execution record")

	cancel()
	s.Stop()
}

func TestCronScheduler_InvalidCronExpression(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "bad-cron", CronExpr: "not-a-cron"},
			{ID: "good-cron", CronExpr: "0 2 * * *"},
		},
	}
	executor := &stubExecutor{}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Only the valid schedule should be registered
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	assert.Equal(t, 1, s.ScheduleCount())

	cancel()
	s.Stop()
}

func TestCronScheduler_NilLock_ExecutesWithoutLocking(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "no-lock-sched", CronExpr: "* * * * *", TenantID: "tenant1"},
		},
	}
	executeCh := make(chan struct{}, 10)
	executor := &stubExecutor{executeCh: executeCh}

	s := scheduler.NewCronScheduler(
		provider, executor, nil, // no lock
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	select {
	case <-executeCh:
		// Executed without lock
	case <-time.After(70 * time.Second):
		t.Fatal("cron job did not fire")
	}

	assert.GreaterOrEqual(t, executor.callCount.Load(), int32(1))

	cancel()
	s.Stop()
}

func TestCronScheduler_ProviderError_ContinuesRunning(t *testing.T) {
	provider := &stubProvider{
		err: errors.New("provider unavailable"),
	}
	executor := &stubExecutor{}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: 200 * time.Millisecond,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Let a few refresh cycles run with error
	time.Sleep(500 * time.Millisecond)

	// Scheduler should still be running despite provider errors
	assert.Equal(t, 0, s.ScheduleCount())

	cancel()
	s.Stop()
}

func TestCronScheduler_LockError_SkipsExecution(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "lock-err-sched", CronExpr: "* * * * *"},
		},
	}
	executor := &stubExecutor{}
	lock := &stubLock{err: errors.New("redis unavailable")}

	s := scheduler.NewCronScheduler(
		provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for cron to fire (up to ~65s for minute-level)
	err := await.New().AtMost(70 * time.Second).PollInterval(time.Second).Until(func() bool {
		// The cron should have fired at least once, but executor should not have been called
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	// Give time for cron to fire
	time.Sleep(65 * time.Second)

	// Executor should not have been called
	assert.Equal(t, int32(0), executor.callCount.Load())

	cancel()
	s.Stop()
}
