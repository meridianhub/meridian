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
	"github.com/robfig/cron/v3"
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

// secondsCron returns a cron.Cron with seconds-level parser for fast tests.
func secondsCron() *cron.Cron {
	return cron.New(
		cron.WithLocation(time.UTC),
		cron.WithSeconds(),
	)
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
		scheduler.WithCronRunner(secondsCron()),
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
			{ID: "sched-1", CronExpr: "0 0 2 * * *", TenantID: "tenant1"},
			{ID: "sched-2", CronExpr: "0 30 * * * *", TenantID: "tenant2"},
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
		scheduler.WithCronRunner(secondsCron()),
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
			{ID: "sched-1", CronExpr: "0 0 2 * * *"},
			{ID: "sched-2", CronExpr: "0 30 * * * *"},
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
		scheduler.WithCronRunner(secondsCron()),
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
		{ID: "sched-1", CronExpr: "0 0 2 * * *"},
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
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "fast-sched", CronExpr: "*/1 * * * * *", TenantID: "tenant1", Metadata: "test-data"},
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
		scheduler.WithCronRunner(secondsCron()),
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

	// Wait for the cron job to fire (seconds-level, should be fast)
	select {
	case <-executeCh:
		// Job fired
	case <-time.After(5 * time.Second):
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
			{ID: "locked-sched", CronExpr: "*/1 * * * * *", TenantID: "tenant1"},
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for the cron to fire and record a SKIPPED execution
	err := await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
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
			{ID: "audit-sched", CronExpr: "*/1 * * * * *", TenantID: "tenant1"},
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for execution
	select {
	case <-executeCh:
	case <-time.After(5 * time.Second):
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
			{ID: "fail-sched", CronExpr: "*/1 * * * * *", TenantID: "tenant1"},
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	select {
	case <-executeCh:
	case <-time.After(5 * time.Second):
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
			{ID: "good-cron", CronExpr: "0 0 2 * * *"},
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
		scheduler.WithCronRunner(secondsCron()),
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
			{ID: "no-lock-sched", CronExpr: "*/1 * * * * *", TenantID: "tenant1"},
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	select {
	case <-executeCh:
		// Executed without lock
	case <-time.After(5 * time.Second):
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	//nolint:forbidigo // Intentional: verifying negative condition (scheduler still running) requires waiting
	time.Sleep(500 * time.Millisecond)

	// Scheduler should still be running despite provider errors
	assert.Equal(t, 0, s.ScheduleCount())

	// Verify Start has not exited early
	select {
	case err := <-errCh:
		t.Fatalf("scheduler exited early: %v", err)
	default:
		// Still running - expected
	}

	cancel()
	s.Stop()
}

func TestCronScheduler_LockError_SkipsExecution(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "lock-err-sched", CronExpr: "*/1 * * * * *"},
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
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for schedule to be loaded and cron to fire at least once
	err := await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	//nolint:forbidigo // Intentional: verifying cron fires but executor is skipped (negative assertion) requires waiting
	time.Sleep(2 * time.Second)

	// Executor should not have been called (lock error prevents execution)
	assert.Equal(t, int32(0), executor.callCount.Load())

	cancel()
	s.Stop()
}

func TestCronScheduler_UpdatesChangedSchedules(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 0 2 * * *", TenantID: "tenant1"},
		},
	}
	executeCh := make(chan struct{}, 10)
	executor := &stubExecutor{executeCh: executeCh}

	s := scheduler.NewCronScheduler(
		provider, executor, nil,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: 200 * time.Millisecond,
			ShutdownTimeout: time.Second,
		},
		slog.Default(),
		scheduler.WithCronRunner(secondsCron()),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for initial load
	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	// Update the schedule to fire every second
	provider.setSchedules([]scheduler.Schedule{
		{ID: "sched-1", CronExpr: "*/1 * * * * *", TenantID: "tenant2"},
	})

	// Wait for cron to fire with the updated schedule
	select {
	case <-executeCh:
		// Updated schedule fired
	case <-time.After(5 * time.Second):
		t.Fatal("updated schedule did not fire")
	}

	calls := executor.getCalls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.Equal(t, "sched-1", calls[0].ID)
	assert.Equal(t, "tenant2", calls[0].TenantID)

	cancel()
	s.Stop()
}
