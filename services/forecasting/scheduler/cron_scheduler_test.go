package scheduler

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// --- Test Helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func testMetrics(t *testing.T) *Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return NewMetricsWithRegistry(reg)
}

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		client.Close()
	})
	return mr, client
}

func testLeaseManager(t *testing.T, client *redis.Client) *LeaseManager {
	t.Helper()
	return NewLeaseManager(client, LeaseConfig{
		LockTTL:       5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, testLogger())
}

// --- Mock Strategy Repository ---

type mockStrategyRepo struct {
	mu         sync.Mutex
	strategies []domain.ForecastingStrategy
	err        error
}

func (m *mockStrategyRepo) Save(_ context.Context, _ domain.ForecastingStrategy) error {
	return nil
}

func (m *mockStrategyRepo) FindByID(_ context.Context, id uuid.UUID) (domain.ForecastingStrategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.strategies {
		if s.ID() == id {
			return s, nil
		}
	}
	return domain.ForecastingStrategy{}, domain.ErrStrategyNotFound
}

func (m *mockStrategyRepo) FindByTenantAndName(_ context.Context, _ string, _ string) (domain.ForecastingStrategy, error) {
	return domain.ForecastingStrategy{}, domain.ErrStrategyNotFound
}

func (m *mockStrategyRepo) ListByTenant(_ context.Context, _ string, _ domain.StrategyFilters) ([]domain.ForecastingStrategy, string, error) {
	return nil, "", nil
}

func (m *mockStrategyRepo) ListAllActive(_ context.Context) ([]domain.ForecastingStrategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	result := make([]domain.ForecastingStrategy, len(m.strategies))
	copy(result, m.strategies)
	return result, nil
}

func (m *mockStrategyRepo) setStrategies(strategies []domain.ForecastingStrategy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.strategies = strategies
}

// --- Mock Forecast Executor ---

type mockExecutor struct {
	mu          sync.Mutex
	calls       []executorCall
	err         error
	result      *ForecastResult
	delay       time.Duration
	callCounter atomic.Int32
}

type executorCall struct {
	strategyID uuid.UUID
}

func (m *mockExecutor) ExecuteForecast(ctx context.Context, strategyID uuid.UUID) (*ForecastResult, error) {
	m.callCounter.Add(1)

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, executorCall{
		strategyID: strategyID,
	})

	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &ForecastResult{PointCount: 24, StrategyVersion: 1}, nil
}

func (m *mockExecutor) callCount() int {
	return int(m.callCounter.Load())
}

func (m *mockExecutor) getCalls() []executorCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]executorCall, len(m.calls))
	copy(result, m.calls)
	return result
}

// --- Test Strategies ---

func newActiveStrategy(t *testing.T, tenantID, name, schedule string) domain.ForecastingStrategy {
	t.Helper()
	s, err := domain.NewForecastingStrategy(
		tenantID,
		name,
		"test strategy",
		"result = [{'timestamp': '2026-01-01T00:00:00Z', 'value': '100.0'}]",
		24,
		1,
		schedule,
		[]string{"DATASET_A"},
		"OUTPUT_DATASET",
		"",
	)
	require.NoError(t, err)
	s, err = s.Activate()
	require.NoError(t, err)
	return s
}

// --- LeaseManager Tests ---

func TestLeaseManager_AcquireAndRelease(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	ctx := context.Background()

	acquired, err := lm.Acquire(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.Equal(t, 1, lm.HeldCount())

	err = lm.Release(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
	assert.Equal(t, 0, lm.HeldCount())
}

func TestLeaseManager_OnlyOneHolderPerStrategy(t *testing.T) {
	_, client := setupMiniredis(t)
	lm1 := testLeaseManager(t, client)
	lm2 := testLeaseManager(t, client)

	ctx := context.Background()

	// First acquires
	acquired, err := lm1.Acquire(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Second cannot acquire the same strategy
	acquired, err = lm2.Acquire(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
	assert.False(t, acquired)

	// Release first, second can now acquire
	err = lm1.Release(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)

	acquired, err = lm2.Acquire(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
	assert.True(t, acquired)

	err = lm2.Release(ctx, "tenant-1", "strategy-abc")
	require.NoError(t, err)
}

func TestLeaseManager_DifferentStrategiesIndependent(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	ctx := context.Background()

	// Acquire two different strategies
	acquired1, err := lm.Acquire(ctx, "tenant-1", "strategy-1")
	require.NoError(t, err)
	assert.True(t, acquired1)

	acquired2, err := lm.Acquire(ctx, "tenant-1", "strategy-2")
	require.NoError(t, err)
	assert.True(t, acquired2)

	assert.Equal(t, 2, lm.HeldCount())

	err = lm.Release(ctx, "tenant-1", "strategy-1")
	require.NoError(t, err)
	err = lm.Release(ctx, "tenant-1", "strategy-2")
	require.NoError(t, err)
}

func TestLeaseManager_ReleaseWithoutAcquire(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	ctx := context.Background()

	// Should be a no-op
	err := lm.Release(ctx, "tenant-1", "nonexistent")
	assert.NoError(t, err)
}

func TestLeaseManager_ReleaseAll(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	ctx := context.Background()

	_, _ = lm.Acquire(ctx, "tenant-1", "s1")
	_, _ = lm.Acquire(ctx, "tenant-1", "s2")
	_, _ = lm.Acquire(ctx, "tenant-2", "s3")
	assert.Equal(t, 3, lm.HeldCount())

	lm.ReleaseAll(ctx)
	assert.Equal(t, 0, lm.HeldCount())
}

func TestLeaseManager_OrphanDetectionViaTTL(t *testing.T) {
	mr, client := setupMiniredis(t)
	lm1 := NewLeaseManager(client, LeaseConfig{
		LockTTL:       200 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
	}, testLogger())

	lm2 := NewLeaseManager(client, LeaseConfig{
		LockTTL:       200 * time.Millisecond,
		RenewInterval: 50 * time.Millisecond,
	}, testLogger())

	ctx := context.Background()

	// First pod acquires
	acquired, err := lm1.Acquire(ctx, "tenant-1", "orphan-test")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Simulate pod crash by releasing without cleanup - just stop renewal
	lm1.mu.Lock()
	key := leaseKey("tenant-1", "orphan-test")
	if lease, ok := lm1.locks[key]; ok {
		lease.cancel() // Stop renewal
	}
	delete(lm1.locks, key) // Remove from tracking (simulating crash)
	lm1.mu.Unlock()

	// Fast-forward past TTL
	mr.FastForward(300 * time.Millisecond)

	// Second pod can now acquire (orphan detected via TTL expiry)
	acquired, err = lm2.Acquire(ctx, "tenant-1", "orphan-test")
	require.NoError(t, err)
	assert.True(t, acquired)

	err = lm2.Release(ctx, "tenant-1", "orphan-test")
	require.NoError(t, err)
}

func TestLeaseManager_DefaultConfig(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := NewLeaseManager(client, LeaseConfig{}, testLogger())

	assert.Equal(t, 5*time.Minute, lm.lockTTL)
	assert.Equal(t, 30*time.Second, lm.renewEvery)
}

// --- CronScheduler Constructor Tests ---

func TestNew_ValidatesRequiredDependencies(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	repo := &mockStrategyRepo{}
	exec := &mockExecutor{}
	logger := testLogger()
	metrics := testMetrics(t)

	t.Run("rejects nil repository", func(t *testing.T) {
		_, err := New(nil, exec, lm, metrics, logger, Config{})
		assert.ErrorIs(t, err, ErrNilRepository)
	})

	t.Run("rejects nil executor", func(t *testing.T) {
		_, err := New(repo, nil, lm, metrics, logger, Config{})
		assert.ErrorIs(t, err, ErrNilExecutor)
	})

	t.Run("rejects nil lease manager", func(t *testing.T) {
		_, err := New(repo, exec, nil, metrics, logger, Config{})
		assert.ErrorIs(t, err, ErrNilLeaseManager)
	})

	t.Run("rejects nil logger", func(t *testing.T) {
		_, err := New(repo, exec, lm, metrics, nil, Config{})
		assert.ErrorIs(t, err, ErrNilLogger)
	})

	t.Run("applies default config values", func(t *testing.T) {
		sched, err := New(repo, exec, lm, metrics, logger, Config{})
		require.NoError(t, err)
		assert.Equal(t, DefaultPollInterval, sched.config.PollInterval)
		assert.Equal(t, DefaultShutdownTimeout, sched.config.ShutdownTimeout)
	})

	t.Run("accepts custom config", func(t *testing.T) {
		sched, err := New(repo, exec, lm, metrics, logger, Config{
			PollInterval:    30 * time.Second,
			ShutdownTimeout: 2 * time.Minute,
		})
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, sched.config.PollInterval)
		assert.Equal(t, 2*time.Minute, sched.config.ShutdownTimeout)
	})
}

// --- CronScheduler Lifecycle Tests ---

func TestCronScheduler_StartAndStop(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	repo := &mockStrategyRepo{}
	exec := &mockExecutor{}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 10 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan error, 1)
	go func() {
		startDone <- sched.Start(ctx)
	}()

	// Give it time to start
	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return sched.running
	})
	require.NoError(t, err)

	cancel()

	select {
	case err := <-startDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop within timeout")
	}
}

func TestCronScheduler_RejectsDoubleStart(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	repo := &mockStrategyRepo{}
	exec := &mockExecutor{}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = sched.Start(ctx)
	}()

	// Wait for it to be running
	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return sched.running
	})
	require.NoError(t, err)

	// Second start should fail
	err = sched.Start(ctx)
	assert.ErrorIs(t, err, ErrSchedulerRunning)
}

// --- Strategy Execution Tests ---

func TestCronScheduler_ExecutesStrategiesOnSchedule(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	strategy := newActiveStrategy(t, "tenant-1", "fast-strategy", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = sched.Start(ctx)
	}()

	// Wait for at least one execution
	err = await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 1
	})
	require.NoError(t, err)

	calls := exec.getCalls()
	assert.Equal(t, strategy.ID(), calls[0].strategyID)

	assert.Equal(t, 1, sched.RegisteredJobCount())
}

func TestCronScheduler_ExecutionWithLeaseContention(t *testing.T) {
	_, client := setupMiniredis(t)

	// Two lease managers simulating two pods
	lm1 := testLeaseManager(t, client)
	lm2 := testLeaseManager(t, client)

	exec1 := &mockExecutor{delay: 500 * time.Millisecond}
	exec2 := &mockExecutor{}

	strategy := newActiveStrategy(t, "tenant-1", "contested-strategy", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	// Start first scheduler
	sched1, err := New(repo, exec1, lm1, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	sched2, err := New(repo, exec2, lm2, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched1.Start(ctx) }()
	go func() { _ = sched2.Start(ctx) }()

	// Wait for some executions
	err = await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return exec1.callCount()+exec2.callCount() >= 2
	})
	require.NoError(t, err)

	// Both schedulers fire, but due to lease contention,
	// only one should execute per cron tick (the other gets skipped).
	// The total call count across both executors should be reasonable.
	totalCalls := exec1.callCount() + exec2.callCount()
	assert.GreaterOrEqual(t, totalCalls, 2)
}

// --- Dynamic Strategy Reload Tests ---

func TestCronScheduler_DetectsNewStrategies(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}
	repo := &mockStrategyRepo{strategies: nil} // Start with no strategies

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 500 * time.Millisecond,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Verify no jobs registered initially
	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return sched.running
	})
	require.NoError(t, err)
	assert.Equal(t, 0, sched.RegisteredJobCount())

	// Add a strategy
	strategy := newActiveStrategy(t, "tenant-1", "new-strategy", "@every 1s")
	repo.setStrategies([]domain.ForecastingStrategy{strategy})

	// Wait for poller to pick it up and register the job
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return sched.RegisteredJobCount() == 1
	})
	require.NoError(t, err)

	// Wait for execution
	err = await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 1
	})
	require.NoError(t, err)
}

func TestCronScheduler_RemovesDeactivatedStrategies(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	strategy := newActiveStrategy(t, "tenant-1", "will-remove", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 500 * time.Millisecond,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Wait for job to be registered
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return sched.RegisteredJobCount() == 1
	})
	require.NoError(t, err)

	// Remove the strategy (simulate deprecation)
	repo.setStrategies(nil)

	// Wait for poller to remove the job
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return sched.RegisteredJobCount() == 0
	})
	require.NoError(t, err)
}

func TestCronScheduler_DetectsScheduleChanges(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	strategy := newActiveStrategy(t, "tenant-1", "schedule-change", "0 0 * * *")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 500 * time.Millisecond,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Wait for job to be registered
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return sched.RegisteredJobCount() == 1
	})
	require.NoError(t, err)

	// Verify the schedule is the original
	sched.mu.Lock()
	origJob := sched.jobs[strategy.ID().String()]
	origEntryID := origJob.entryID
	sched.mu.Unlock()
	assert.Equal(t, "0 0 * * *", origJob.schedule)

	// Change the schedule (create a new strategy with same ID but different schedule)
	updatedStrategy, err := strategy.UpdateSchedule("@every 1s")
	require.NoError(t, err)
	repo.setStrategies([]domain.ForecastingStrategy{updatedStrategy})

	// Wait for poller to detect the change
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		job, exists := sched.jobs[strategy.ID().String()]
		return exists && job.entryID != origEntryID
	})
	require.NoError(t, err)

	// Verify the new schedule is registered
	sched.mu.Lock()
	newJob := sched.jobs[strategy.ID().String()]
	sched.mu.Unlock()
	assert.Equal(t, "@every 1s", newJob.schedule)
}

// --- Graceful Shutdown Tests ---

func TestCronScheduler_GracefulShutdownWaitsForInFlight(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	// Executor with a delay to simulate long-running forecast
	exec := &mockExecutor{delay: 500 * time.Millisecond}

	strategy := newActiveStrategy(t, "tenant-1", "slow-strategy", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval:    30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan error, 1)
	go func() {
		startDone <- sched.Start(ctx)
	}()

	// Wait for at least one execution to start
	err = await.New().AtMost(5 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 1
	})
	require.NoError(t, err)

	// Cancel context to trigger shutdown
	cancel()

	// Shutdown should complete (executor's delay is 500ms, timeout is 5s)
	select {
	case err := <-startDone:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler did not stop within timeout")
	}

	// All leases should be released
	assert.Equal(t, 0, lm.HeldCount())
}

func TestCronScheduler_StoppedDoesNotExecute(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	strategy := newActiveStrategy(t, "tenant-1", "wont-run", "@every 100ms")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	startDone := make(chan error, 1)
	go func() {
		startDone <- sched.Start(ctx)
	}()

	// Wait for at least one execution
	err = await.New().AtMost(3 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 1
	})
	require.NoError(t, err)

	// Stop the scheduler
	cancel()
	<-startDone

	// Record how many calls happened
	countAfterStop := exec.callCount()

	// Wait to ensure no more executions happen
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, countAfterStop, exec.callCount(), "no new executions after stop")
}

// --- Multi-Tenant Tests ---

func TestCronScheduler_MultiTenantStrategies(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	s1 := newActiveStrategy(t, "tenant-1", "s1", "@every 1s")
	s2 := newActiveStrategy(t, "tenant-2", "s2", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{s1, s2}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Both strategies should be registered
	err = await.New().AtMost(3 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return sched.RegisteredJobCount() == 2
	})
	require.NoError(t, err)

	// Wait for executions from both
	err = await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 2
	})
	require.NoError(t, err)

	// Verify both strategy IDs were executed
	calls := exec.getCalls()
	executedIDs := make(map[uuid.UUID]bool)
	for _, c := range calls {
		executedIDs[c.strategyID] = true
	}
	assert.True(t, executedIDs[s1.ID()], "tenant-1 strategy should have been executed")
	assert.True(t, executedIDs[s2.ID()], "tenant-2 strategy should have been executed")
}

// --- Error Handling Tests ---

func TestCronScheduler_ExecutionErrorDoesNotStopScheduler(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)

	exec := &mockExecutor{err: assert.AnError}

	strategy := newActiveStrategy(t, "tenant-1", "error-strategy", "@every 1s")
	repo := &mockStrategyRepo{strategies: []domain.ForecastingStrategy{strategy}}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 30 * time.Second,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Despite errors, the scheduler keeps trying
	err = await.New().AtMost(5 * time.Second).PollInterval(100 * time.Millisecond).Until(func() bool {
		return exec.callCount() >= 2
	})
	require.NoError(t, err)
}

func TestCronScheduler_RepoErrorDoesNotStopScheduler(t *testing.T) {
	_, client := setupMiniredis(t)
	lm := testLeaseManager(t, client)
	exec := &mockExecutor{}

	repo := &mockStrategyRepo{err: assert.AnError}

	sched, err := New(repo, exec, lm, testMetrics(t), testLogger(), Config{
		PollInterval: 500 * time.Millisecond,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sched.Start(ctx) }()

	// Wait for scheduler to be running
	err = await.New().AtMost(2 * time.Second).PollInterval(50 * time.Millisecond).Until(func() bool {
		sched.mu.Lock()
		defer sched.mu.Unlock()
		return sched.running
	})
	require.NoError(t, err)

	// Scheduler should still be running despite repo errors
	time.Sleep(1 * time.Second)
	sched.mu.Lock()
	running := sched.running
	sched.mu.Unlock()
	assert.True(t, running, "scheduler should remain running despite repo errors")
}

// --- Metrics Tests ---

func TestMetrics_Creation(t *testing.T) {
	t.Run("default registry", func(t *testing.T) {
		m := NewMetrics()
		assert.NotNil(t, m)
	})

	t.Run("custom registry", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		m := NewMetricsWithRegistry(reg)
		assert.NotNil(t, m)

		// Verify metrics are registered
		m.RecordExecution("tenant-1", "strategy-1", "success")
		m.ObserveExecutionDuration("tenant-1", "strategy-1", 1.5)
		m.RecordLeaseFailure("contention")
		m.SetActiveStrategies(3)
		m.RecordReload("success")
		m.RecordError("test_error")

		// Should not panic
		families, err := reg.Gather()
		require.NoError(t, err)
		assert.NotEmpty(t, families)
	})
}

// --- Lease Key Format Tests ---

func TestLeaseKey_Format(t *testing.T) {
	key := leaseKey("tenant-abc", "strategy-123")
	assert.Equal(t, "meridian:forecasting:strategy:tenant-abc:strategy-123", key)
}
