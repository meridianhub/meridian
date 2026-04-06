package scheduler_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errStatusUnavailable = errors.New("status service unavailable")

// secondsParser is a cron.Parser matching the seconds-level cron runner used in tests.
var secondsParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// expectedWindowCount counts how many cron windows exist between start (exclusive) and end (inclusive).
func expectedWindowCount(t *testing.T, cronExpr string, start, end time.Time) int {
	t.Helper()
	sched, err := secondsParser.Parse(cronExpr)
	require.NoError(t, err)

	count := 0
	next := sched.Next(start)
	for !next.After(end) {
		count++
		next = sched.Next(next)
	}
	return count
}

// newTestScheduler creates a CronScheduler configured for testing with the
// seconds-level cron runner and matching parser.
func newTestScheduler(
	provider scheduler.ScheduleProvider,
	executor scheduler.Executor,
	lock scheduler.DistributedLock,
	config scheduler.CronSchedulerConfig,
	opts ...scheduler.CronSchedulerOption,
) *scheduler.CronScheduler {
	allOpts := make([]scheduler.CronSchedulerOption, 0, 2+len(opts))
	allOpts = append(allOpts,
		scheduler.WithCronRunner(secondsCron()),
		scheduler.WithCronParser(secondsParser),
	)
	allOpts = append(allOpts, opts...)
	return scheduler.NewCronScheduler(
		provider, executor, lock, config, slog.Default(), allOpts...,
	)
}

func TestCatchUp_NoPriorExecution_CatchesUpFromMaxAge(t *testing.T) {
	// Schedule runs every 10 minutes. With 1h MaxCatchUpAge and no prior execution,
	// catch-up should execute ~6 windows (every 10 min for 1 hour).
	cronExpr := "0 */10 * * * *"
	store := &stubExecutionStore{}
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executeCh := make(chan struct{}, 100)
	executor := &stubExecutor{executeCh: executeCh}
	lock := &stubLock{acquired: true}

	maxCatchUpAge := time.Hour

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   maxCatchUpAge,
		},
		scheduler.WithCronExecutionStore(store),
	)

	// Capture now before starting the scheduler to avoid race at cron boundaries.
	now := time.Now().UTC()
	expected := expectedWindowCount(t, cronExpr, now.Add(-maxCatchUpAge), now)
	require.Greater(t, expected, 0, "should have at least one expected window")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(expected)
	})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expected)

	cancel()
	s.Stop()
}

func TestCatchUp_RecentExecution_NoCatchUpNeeded(t *testing.T) {
	// Schedule runs every 10 minutes. Seed the last execution at the most recent
	// cron window so there are zero missed windows between seed and now.
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()

	// Find the most recent cron window at or before now.
	sched, err := secondsParser.Parse(cronExpr)
	require.NoError(t, err)
	// Walk backwards: find the window just before now by checking next-after(now - 10m).
	recentWindow := sched.Next(now.Add(-10 * time.Minute))
	// If recentWindow is after now, step back one more interval.
	if recentWindow.After(now) {
		recentWindow = sched.Next(now.Add(-20 * time.Minute))
	}

	store := &stubExecutionStore{}
	recentExec := scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   recentWindow,
		Status:        scheduler.ExecutionStatusCompleted,
	}
	_ = store.RecordExecution(context.Background(), recentExec)

	// Verify our assumption: no cron windows between recentWindow and now.
	nextAfterSeed := sched.Next(recentWindow)
	require.True(t, nextAfterSeed.After(now),
		"seed should be the most recent window; next window %v should be after now %v", nextAfterSeed, now)

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{}
	lock := &stubLock{acquired: true}

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	//nolint:forbidigo // Intentional: verifying no catch-up occurs (negative assertion) requires waiting
	time.Sleep(200 * time.Millisecond)

	// Executor should not have been called by catch-up.
	assert.Equal(t, int32(0), executor.callCount.Load(),
		"no catch-up executions expected when last execution is recent")

	cancel()
	s.Stop()
}

func TestCatchUp_StaleExecution_CatchesUpFromLastExec(t *testing.T) {
	// Schedule runs every 10 minutes. Last execution was 30 minutes ago.
	// Should catch up ~3 windows (10m, 20m, 30m ago range).
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-30 * time.Minute)

	store := &stubExecutionStore{}
	staleExec := scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	}
	_ = store.RecordExecution(context.Background(), staleExec)

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 100)}
	lock := &stubLock{acquired: true}

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
	)

	expected := expectedWindowCount(t, cronExpr, lastExecTime, now)
	require.Greater(t, expected, 0)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(expected)
	})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expected)

	cancel()
	s.Stop()
}

func TestCatchUp_VeryOldExecution_RecordsMissedAndExecutesRecent(t *testing.T) {
	// Schedule runs every 10 minutes. Last execution was 2 hours ago.
	// MaxCatchUpAge is 30 minutes.
	// Windows between 2h and 30m ago: MISSED (audit only).
	// Windows within 30m: executed.
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-2 * time.Hour)
	maxCatchUpAge := 30 * time.Minute
	catchUpCutoff := now.Add(-maxCatchUpAge)

	store := &stubExecutionStore{}
	oldExec := scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	}
	_ = store.RecordExecution(context.Background(), oldExec)

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 100)}
	lock := &stubLock{acquired: true}

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   maxCatchUpAge,
		},
		scheduler.WithCronExecutionStore(store),
	)

	expectedExecuted := expectedWindowCount(t, cronExpr, catchUpCutoff, now)
	expectedMissed := expectedWindowCount(t, cronExpr, lastExecTime, catchUpCutoff)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait for all catch-up processing (executed + missed should appear in store).
	totalExpected := expectedExecuted + expectedMissed
	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		execs := store.getExecutions()
		// Subtract the seed execution.
		catchUpRecords := len(execs) - 1
		return catchUpRecords >= totalExpected
	})
	require.NoError(t, err)

	// Verify executor was called only for recent windows.
	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expectedExecuted)

	// Verify MISSED records exist.
	execs := store.getExecutions()
	missedCount := 0
	for _, e := range execs {
		if e.Status == scheduler.ExecutionStatusMissed {
			missedCount++
		}
	}
	assert.GreaterOrEqual(t, missedCount, expectedMissed,
		"should have MISSED records for windows beyond MaxCatchUpAge")

	cancel()
	s.Stop()
}

func TestCatchUp_NoExecutionStore_IsNoOp(t *testing.T) {
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 */10 * * * *", TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{}
	lock := &stubLock{acquired: true}

	// No execution store configured.
	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return s.ScheduleCount() == 1
	})
	require.NoError(t, err)

	//nolint:forbidigo // Intentional: verifying no catch-up occurs without a store (negative assertion) requires waiting
	time.Sleep(200 * time.Millisecond)

	// Executor should not have been called by catch-up (no store = no catch-up).
	assert.Equal(t, int32(0), executor.callCount.Load())

	cancel()
	s.Stop()
}

func TestCatchUp_MultipleSchedules_IndependentCatchUp(t *testing.T) {
	// Two schedules with different cron expressions and different staleness.
	now := time.Now().UTC()
	maxCatchUpAge := time.Hour

	store := &stubExecutionStore{}
	// sched-1: last executed 40 minutes ago, runs every 10 min
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   now.Add(-40 * time.Minute),
		Status:        scheduler.ExecutionStatusCompleted,
	})
	// sched-2: last executed 20 minutes ago, runs every 15 min
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-2",
		ScheduledAt:   now.Add(-20 * time.Minute),
		Status:        scheduler.ExecutionStatusCompleted,
	})

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 */10 * * * *", TenantID: "tenant1"},
			{ID: "sched-2", CronExpr: "0 */15 * * * *", TenantID: "tenant1"},
		},
	}

	var mu sync.Mutex
	callsBySchedule := make(map[string]int)
	executor := &stubExecutor{
		executeFunc: func(_ context.Context, schedule scheduler.Schedule) error {
			mu.Lock()
			callsBySchedule[schedule.ID]++
			mu.Unlock()
			return nil
		},
	}
	lock := &stubLock{acquired: true}

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   maxCatchUpAge,
		},
		scheduler.WithCronExecutionStore(store),
	)

	expected1 := expectedWindowCount(t, "0 */10 * * * *", now.Add(-40*time.Minute), now)
	expected2 := expectedWindowCount(t, "0 */15 * * * *", now.Add(-20*time.Minute), now)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	totalExpected := expected1 + expected2
	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(totalExpected)
	})
	require.NoError(t, err)

	mu.Lock()
	calls1 := callsBySchedule["sched-1"]
	calls2 := callsBySchedule["sched-2"]
	mu.Unlock()

	assert.GreaterOrEqual(t, calls1, expected1,
		"sched-1 should have caught up independently")
	assert.GreaterOrEqual(t, calls2, expected2,
		"sched-2 should have caught up independently")

	cancel()
	s.Stop()
}

func TestCatchUp_TenantStatusChecker_ActiveTenant_CatchUpExecutes(t *testing.T) {
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-30 * time.Minute)

	store := &stubExecutionStore{}
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	})

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 100)}
	lock := &stubLock{acquired: true}
	checker := &stubTenantStatusChecker{active: true}

	expected := expectedWindowCount(t, cronExpr, lastExecTime, now)
	require.Greater(t, expected, 0)

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
		scheduler.WithTenantStatusChecker(checker),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(expected)
	})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expected)

	cancel()
	s.Stop()
}

func TestCatchUp_TenantStatusChecker_InactiveTenant_CatchUpSkipped(t *testing.T) {
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-30 * time.Minute)

	store := &stubExecutionStore{}
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	})

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "inactive-tenant"},
		},
	}
	executor := &stubExecutor{}
	lock := &stubLock{acquired: true}
	checker := &stubTenantStatusChecker{active: false}

	expected := expectedWindowCount(t, cronExpr, lastExecTime, now)
	require.Greater(t, expected, 0)

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
		scheduler.WithTenantStatusChecker(checker),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Wait until all catch-up windows are recorded as SKIPPED (one per window)
	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		count := 0
		for _, e := range store.getExecutions() {
			if e.Status == scheduler.ExecutionStatusSkipped &&
				e.ErrorMessage != nil && *e.ErrorMessage == "tenant not active" {
				count++
			}
		}
		return count >= expected
	})
	require.NoError(t, err)

	assert.Equal(t, int32(0), executor.callCount.Load(),
		"executor must not be called for inactive tenant during catch-up")

	cancel()
	s.Stop()
}

func TestCatchUp_TenantStatusChecker_CheckError_CatchUpProceeds(t *testing.T) {
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-30 * time.Minute)

	store := &stubExecutionStore{}
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	})

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 100)}
	lock := &stubLock{acquired: true}
	checker := &stubTenantStatusChecker{err: errStatusUnavailable}

	expected := expectedWindowCount(t, cronExpr, lastExecTime, now)
	require.Greater(t, expected, 0)

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
		scheduler.WithTenantStatusChecker(checker),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	// Fail open: catch-up proceeds despite status check error
	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(expected)
	})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expected)

	cancel()
	s.Stop()
}

func TestCatchUp_TenantStatusChecker_NoChecker_CatchUpProceeds(t *testing.T) {
	cronExpr := "0 */10 * * * *"
	now := time.Now().UTC()
	lastExecTime := now.Add(-30 * time.Minute)

	store := &stubExecutionStore{}
	_ = store.RecordExecution(context.Background(), scheduler.Execution{
		SchedulerName: "test-scheduler",
		ScheduleID:    "sched-1",
		ScheduledAt:   lastExecTime,
		Status:        scheduler.ExecutionStatusCompleted,
	})

	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: cronExpr, TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{executeCh: make(chan struct{}, 100)}
	lock := &stubLock{acquired: true}

	expected := expectedWindowCount(t, cronExpr, lastExecTime, now)
	require.Greater(t, expected, 0)

	// No WithTenantStatusChecker option
	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	err := await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return executor.callCount.Load() >= int32(expected)
	})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, int(executor.callCount.Load()), expected)

	cancel()
	s.Stop()
}

func TestCatchUp_LockNotAcquired_SkipsCatchUp(t *testing.T) {
	// When the catch-up lock cannot be acquired, no catch-up should happen.
	store := &stubExecutionStore{}
	provider := &stubProvider{
		schedules: []scheduler.Schedule{
			{ID: "sched-1", CronExpr: "0 */10 * * * *", TenantID: "tenant1"},
		},
	}
	executor := &stubExecutor{}
	lock := &stubLock{acquired: false}

	s := newTestScheduler(provider, executor, lock,
		scheduler.CronSchedulerConfig{
			Name:            "test-scheduler",
			RefreshInterval: time.Hour,
			ShutdownTimeout: 2 * time.Second,
			MaxCatchUpAge:   time.Hour,
		},
		scheduler.WithCronExecutionStore(store),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	//nolint:forbidigo // Intentional: lock failure prevents observable state; waiting to confirm no catch-up occurs
	time.Sleep(500 * time.Millisecond)

	// No catch-up records should exist.
	execs := store.getExecutions()
	assert.Empty(t, execs, "no catch-up records when lock not acquired")
	assert.Equal(t, int32(0), executor.callCount.Load())

	cancel()
	s.Stop()
}
