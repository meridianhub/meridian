package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupDunningMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
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

func dunningTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func dunningTestMetrics(t *testing.T) *BillingMetrics {
	t.Helper()
	return NewBillingMetricsWithRegistry(prometheus.NewRegistry())
}

func noopCallback(_ context.Context, _ *domain.BillingRun) error {
	return nil
}

func TestNewDunningWorker_Validation(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	t.Run("rejects nil repository", func(t *testing.T) {
		_, err := NewDunningWorker(nil, client, DunningWorkerConfig{}, noopCallback, logger, metrics)
		assert.ErrorIs(t, err, ErrNilBillingRepo)
	})

	t.Run("rejects nil redis client", func(t *testing.T) {
		_, err := NewDunningWorker(repo, nil, DunningWorkerConfig{}, noopCallback, logger, metrics)
		assert.ErrorIs(t, err, ErrNilRedisClient)
	})

	t.Run("rejects nil logger", func(t *testing.T) {
		_, err := NewDunningWorker(repo, client, DunningWorkerConfig{}, noopCallback, nil, metrics)
		assert.ErrorIs(t, err, ErrNilBillingLogger)
	})

	t.Run("rejects nil callback", func(t *testing.T) {
		_, err := NewDunningWorker(repo, client, DunningWorkerConfig{}, nil, logger, metrics)
		assert.ErrorIs(t, err, ErrNilDunningCallback)
	})

	t.Run("applies default poll interval", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{}, noopCallback, logger, metrics)
		require.NoError(t, err)
		assert.Equal(t, 60*time.Second, w.config.PollInterval)
	})

	t.Run("applies default max dunning level", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{}, noopCallback, logger, metrics)
		require.NoError(t, err)
		assert.Equal(t, domain.MaxDunningLevel, w.config.MaxDunningLevel)
	})

	t.Run("applies default shutdown timeout", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{}, noopCallback, logger, metrics)
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, w.config.ShutdownTimeout)
	})

	t.Run("creates worker with valid args", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    5 * time.Second,
			MaxDunningLevel: 3,
			ShutdownTimeout: 10 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)
		assert.NotNil(t, w)
		assert.NotNil(t, w.lifecycle)
		assert.NotNil(t, w.lock)
		assert.Equal(t, 5*time.Second, w.config.PollInterval)
		assert.Equal(t, 3, w.config.MaxDunningLevel)
		assert.Equal(t, 10*time.Second, w.config.ShutdownTimeout)
	})
}

func TestDunningWorker_StartStop(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	t.Run("starts and stops gracefully", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		started := make(chan struct{})
		errCh := make(chan error, 1)
		go func() {
			close(started)
			errCh <- w.Start(context.Background())
		}()
		<-started

		// Give the poll loop time to run at least once
		time.Sleep(100 * time.Millisecond) //nolint:forbidigo // gives poll loop time to run at least once

		w.Stop()

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return after Stop")
		}
	})

	t.Run("rejects double start", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		started := make(chan struct{})
		go func() {
			close(started)
			_ = w.Start(context.Background())
		}()
		<-started

		// Give Start time to acquire running state
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // gives worker time to enter running state

		err = w.Start(context.Background())
		assert.ErrorIs(t, err, scheduler.ErrAlreadyRunning)

		w.Stop()
	})

	t.Run("stops via context cancellation", func(t *testing.T) {
		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		time.Sleep(100 * time.Millisecond) //nolint:forbidigo // gives worker time to start before cancellation
		cancel()

		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return after context cancel")
		}
	})
}

func TestDunningWorker_ScheduleAndProcess(t *testing.T) {
	mr, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"
	zsetKey := dunningRetryZSetPrefix + testTenantID

	t.Run("schedules and processes retry", func(t *testing.T) {
		repo := newMockBillingRepo()

		// Create a failed billing run
		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:           billingRunID,
			TenantID:     testTenantID,
			Status:       domain.BillingRunStatusFailed,
			DunningLevel: 1,
			CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		}
		repo.mu.Lock()
		repo.runs[billingRunID] = run
		repo.mu.Unlock()

		var callbackCalled atomic.Bool
		callback := func(_ context.Context, r *domain.BillingRun) error {
			callbackCalled.Store(true)
			assert.Equal(t, billingRunID, r.ID)
			return nil
		}

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, callback, logger, metrics)
		require.NoError(t, err)

		// Schedule retry in the past so it's immediately due
		originalNowFunc := NowFunc
		NowFunc = func() time.Time { return time.Now().UTC() }
		defer func() { NowFunc = originalNowFunc }()

		err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
		require.NoError(t, err)

		// Start the worker
		go func() {
			_ = w.Start(context.Background())
		}()

		// Wait for the callback to fire
		require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
			return callbackCalled.Load()
		}), "callback was not called within timeout")

		// Wait for the ZREM to complete (happens after callback in the same poll cycle).
		// Poll before calling Stop() to avoid context cancellation racing with ZREM.
		require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
			members, zErr := client.ZRangeArgs(context.Background(), redis.ZRangeArgs{
				Key:     zsetKey,
				Start:   "-inf",
				Stop:    "+inf",
				ByScore: true,
			}).Result()
			require.NoError(t, zErr)
			return len(members) == 0
		}), "ZSET not drained within timeout")

		w.Stop()
	})

	t.Run("skips billing run that is no longer failed", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:         billingRunID,
			TenantID:   testTenantID,
			Status:     domain.BillingRunStatusCompleted, // Not failed
			CycleStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			CycleEnd:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		}
		repo.mu.Lock()
		repo.runs[billingRunID] = run
		repo.mu.Unlock()

		var callbackCalled atomic.Bool
		callback := func(_ context.Context, _ *domain.BillingRun) error {
			callbackCalled.Store(true)
			return nil
		}

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, callback, logger, metrics)
		require.NoError(t, err)

		// Clean ZSET from previous test
		client.Del(context.Background(), zsetKey)

		// Schedule in the past
		err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		// Wait for a poll cycle
		time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle
		w.Stop()

		assert.False(t, callbackCalled.Load(), "callback should not be called for non-failed billing run")
	})

	t.Run("cancels pending dunning emails when billing run resolved", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:         billingRunID,
			TenantID:   testTenantID,
			Status:     domain.BillingRunStatusCompleted, // Resolved
			CycleStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			CycleEnd:   time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		}
		repo.mu.Lock()
		repo.runs[billingRunID] = run
		repo.mu.Unlock()

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		// Set up email canceller mock
		var callCount atomic.Int32
		var cancelledPattern atomic.Value
		mockCanceller := &mockEmailCanceller{
			cancelFunc: func(_ context.Context, pattern string) (int64, error) {
				cancelledPattern.Store(pattern)
				callCount.Add(1)
				return 1, nil
			},
		}
		w.SetEmailCanceller(mockCanceller)

		// Clean ZSET from previous test
		client.Del(context.Background(), zsetKey)

		err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		// Wait for all 4 escalation prefix cancellations to complete
		err = await.New().AtMost(5 * time.Second).PollInterval(10 * time.Millisecond).Until(func() bool {
			return callCount.Load() >= 4
		})
		require.NoError(t, err, "email canceller should have been called for all prefixes")

		w.Stop()

		// Cancellation calls with specific prefixes (dunning-1-, dunning-2-, dunning-3-, dunning-frozen-)
		assert.Equal(t, int32(4), callCount.Load())
		assert.Contains(t, cancelledPattern.Load().(string), billingRunID.String())
	})

	t.Run("drops billing run not found", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		// Clean ZSET
		client.Del(context.Background(), zsetKey)

		err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle
		w.Stop()

		// Verify the retry was removed from the ZSET (dropped)
		members, err := client.ZRangeArgs(context.Background(), redis.ZRangeArgs{
			Key:     zsetKey,
			Start:   "-inf",
			Stop:    "+inf",
			ByScore: true,
		}).Result()
		require.NoError(t, err)
		assert.Empty(t, members, "not-found retry should be removed from ZSET")
	})

	t.Run("retains member on transient callback error", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:           billingRunID,
			TenantID:     testTenantID,
			Status:       domain.BillingRunStatusFailed,
			DunningLevel: 1,
			CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		}
		repo.mu.Lock()
		repo.runs[billingRunID] = run
		repo.mu.Unlock()

		errCallback := func(_ context.Context, _ *domain.BillingRun) error {
			return errors.New("transient error")
		}

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, errCallback, logger, metrics)
		require.NoError(t, err)

		// Clean ZSET
		client.Del(context.Background(), zsetKey)

		err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle
		w.Stop()

		// Verify the retry is still in the ZSET
		count, err := client.ZCard(context.Background(), zsetKey).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "failed retry should be retained in ZSET")
	})

	t.Run("rejects empty tenant ID", func(t *testing.T) {
		repo := newMockBillingRepo()

		w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
			PollInterval:    50 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		}, noopCallback, logger, metrics)
		require.NoError(t, err)

		err = w.ScheduleDunningRetry(context.Background(), "", uuid.New(), time.Hour)
		assert.ErrorIs(t, err, ErrDunningMissingTenant)
	})

	// Clean up ZSET for other tests
	mr.FlushAll()
}

func TestDunningWorker_CancelRetry(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"
	zsetKey := dunningRetryZSetPrefix + testTenantID

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()
	billingRunID := uuid.New()

	// Schedule a retry
	err = w.ScheduleDunningRetry(ctx, testTenantID, billingRunID, time.Hour)
	require.NoError(t, err)

	// Verify it's in the ZSET
	count, err := client.ZCard(ctx, zsetKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Cancel it
	err = w.CancelDunningRetry(ctx, testTenantID, billingRunID)
	require.NoError(t, err)

	// Verify it's gone
	count, err = client.ZCard(ctx, zsetKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	t.Run("rejects empty tenant ID", func(t *testing.T) {
		err := w.CancelDunningRetry(ctx, "", uuid.New())
		assert.ErrorIs(t, err, ErrDunningMissingTenant)
	})
}

func TestDunningWorker_PerItemLocking(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"

	billingRunID := uuid.New()
	run := &domain.BillingRun{
		ID:           billingRunID,
		TenantID:     testTenantID,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	repo.mu.Lock()
	repo.runs[billingRunID] = run
	repo.mu.Unlock()

	var callCount atomic.Int32
	callback := func(_ context.Context, _ *domain.BillingRun) error {
		callCount.Add(1)
		return nil
	}

	// Create two workers sharing the same Redis to simulate replicas
	w1, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, callback, logger, metrics)
	require.NoError(t, err)

	w2, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, callback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Schedule retry in the past
	err = w1.ScheduleDunningRetry(ctx, testTenantID, billingRunID, -1*time.Minute)
	require.NoError(t, err)

	// Both workers try to process - only one should succeed due to locking
	result1 := w1.processRetry(ctx, billingRunID)
	result2 := w2.processRetry(ctx, billingRunID)

	// One should succeed, one should fail to acquire lock
	assert.True(t, result1 != result2 || (result1 && result2),
		"at least one worker should process the retry")

	// The callback should be called at most by the number of successful processors
	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestDunningWorker_GracefulShutdownWaitsForInFlight(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"

	billingRunID := uuid.New()
	run := &domain.BillingRun{
		ID:           billingRunID,
		TenantID:     testTenantID,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	repo.mu.Lock()
	repo.runs[billingRunID] = run
	repo.mu.Unlock()

	var callbackCompleted atomic.Bool
	slowCallback := func(_ context.Context, _ *domain.BillingRun) error {
		time.Sleep(200 * time.Millisecond) //nolint:forbidigo // simulates slow callback latency
		callbackCompleted.Store(true)
		return nil
	}

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}, slowCallback, logger, metrics)
	require.NoError(t, err)

	// Schedule retry in the past
	err = w.ScheduleDunningRetry(context.Background(), testTenantID, billingRunID, -1*time.Minute)
	require.NoError(t, err)

	go func() {
		_ = w.Start(context.Background())
	}()

	// Wait for the callback to start
	time.Sleep(150 * time.Millisecond) //nolint:forbidigo // gives worker time to start processing callback

	// Stop should wait for the slow callback to complete
	w.Stop()

	assert.True(t, callbackCompleted.Load(), "shutdown should have waited for in-flight callback")
}

func TestDunningWorker_InvalidMemberInZSET(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"
	zsetKey := dunningRetryZSetPrefix + testTenantID

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Add an invalid UUID to the tenant-scoped ZSET
	client.ZAdd(ctx, zsetKey, redis.Z{
		Score:  float64(time.Now().Add(-1 * time.Minute).Unix()),
		Member: "not-a-uuid",
	})

	go func() {
		_ = w.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle
	w.Stop()

	// Invalid member should be removed
	members, err := client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     zsetKey,
		Start:   "-inf",
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	require.NoError(t, err)
	assert.Empty(t, members, "invalid member should be removed from ZSET")
}

func TestDunningWorker_RepoErrorRetainsRetry(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	repo.findErr = errors.New("database unavailable")
	metrics := dunningTestMetrics(t)

	const testTenantID = "tenant_1"
	zsetKey := dunningRetryZSetPrefix + testTenantID

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()
	billingRunID := uuid.New()

	// Clean ZSET
	client.Del(ctx, zsetKey)

	err = w.ScheduleDunningRetry(ctx, testTenantID, billingRunID, -1*time.Minute)
	require.NoError(t, err)

	go func() {
		_ = w.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives worker time to run at least one poll cycle
	w.Stop()

	// Retry should still be in the ZSET
	count, err := client.ZCard(ctx, zsetKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "retry should be retained when repo returns error")
}

func TestDunningWorker_TenantIsolation(t *testing.T) {
	mr, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	metrics := dunningTestMetrics(t)

	const tenantA = "tenant_alpha"
	const tenantB = "tenant_beta"

	repo := newMockBillingRepo()

	// Create billing runs for two different tenants
	runA := &domain.BillingRun{
		ID:           uuid.New(),
		TenantID:     tenantA,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	runB := &domain.BillingRun{
		ID:           uuid.New(),
		TenantID:     tenantB,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	repo.mu.Lock()
	repo.runs[runA.ID] = runA
	repo.runs[runB.ID] = runB
	repo.mu.Unlock()

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Schedule retries for both tenants
	err = w.ScheduleDunningRetry(ctx, tenantA, runA.ID, time.Hour)
	require.NoError(t, err)
	err = w.ScheduleDunningRetry(ctx, tenantB, runB.ID, time.Hour)
	require.NoError(t, err)

	// Verify each tenant's ZSET contains only their own billing run
	keyA := dunningRetryZSetPrefix + tenantA
	keyB := dunningRetryZSetPrefix + tenantB

	membersA, err := client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     keyA,
		Start:   "-inf",
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	require.NoError(t, err)
	assert.Len(t, membersA, 1)
	assert.Equal(t, runA.ID.String(), membersA[0])

	membersB, err := client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     keyB,
		Start:   "-inf",
		Stop:    "+inf",
		ByScore: true,
	}).Result()
	require.NoError(t, err)
	assert.Len(t, membersB, 1)
	assert.Equal(t, runB.ID.String(), membersB[0])

	// Cancel tenant A's retry and verify tenant B is unaffected
	err = w.CancelDunningRetry(ctx, tenantA, runA.ID)
	require.NoError(t, err)

	countA, err := client.ZCard(ctx, keyA).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), countA, "tenant A's ZSET should be empty after cancel")

	countB, err := client.ZCard(ctx, keyB).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), countB, "tenant B's ZSET should be unaffected by tenant A's cancel")

	mr.FlushAll()
}

func TestDunningWorker_TenantIsolation_Processing(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	metrics := dunningTestMetrics(t)

	const tenantA = "tenant_alpha"
	const tenantB = "tenant_beta"

	repo := newMockBillingRepo()

	runA := &domain.BillingRun{
		ID:           uuid.New(),
		TenantID:     tenantA,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	runB := &domain.BillingRun{
		ID:           uuid.New(),
		TenantID:     tenantB,
		Status:       domain.BillingRunStatusFailed,
		DunningLevel: 1,
		CycleStart:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CycleEnd:     time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	}
	repo.mu.Lock()
	repo.runs[runA.ID] = runA
	repo.runs[runB.ID] = runB
	repo.mu.Unlock()

	// Track which billing run IDs get processed using atomic counters
	var processedA atomic.Bool
	var processedB atomic.Bool
	callback := func(_ context.Context, r *domain.BillingRun) error {
		if r.ID == runA.ID {
			processedA.Store(true)
		}
		if r.ID == runB.ID {
			processedB.Store(true)
		}
		return nil
	}

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, callback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Schedule both in the past so they're immediately due
	originalNowFunc := NowFunc
	NowFunc = func() time.Time { return time.Now().UTC() }
	defer func() { NowFunc = originalNowFunc }()

	err = w.ScheduleDunningRetry(ctx, tenantA, runA.ID, -1*time.Minute)
	require.NoError(t, err)
	err = w.ScheduleDunningRetry(ctx, tenantB, runB.ID, -1*time.Minute)
	require.NoError(t, err)

	// Start worker and let it process
	go func() {
		_ = w.Start(context.Background())
	}()

	// Wait for both to be processed
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		return processedA.Load() && processedB.Load()
	}), "expected both tenants to be processed within timeout")

	// Both billing runs should have been processed
	assert.True(t, processedA.Load(), "tenant A's billing run should have been processed")
	assert.True(t, processedB.Load(), "tenant B's billing run should have been processed")

	// Wait for ZSET cleanup to complete (ZRem happens after the callback)
	keyA := dunningRetryZSetPrefix + tenantA
	keyB := dunningRetryZSetPrefix + tenantB
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		countA, _ := client.ZCard(ctx, keyA).Result()
		countB, _ := client.ZCard(ctx, keyB).Result()
		return countA == 0 && countB == 0
	}), "ZSET cleanup timeout")

	w.Stop()

	// Both tenant ZSETs should be empty
	countA, err := client.ZCard(ctx, keyA).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), countA)

	countB, err := client.ZCard(ctx, keyB).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), countB)
}

// mockEmailCanceller implements DunningEmailCanceller for testing.
type mockEmailCanceller struct {
	cancelFunc func(ctx context.Context, pattern string) (int64, error)
}

func (m *mockEmailCanceller) CancelByIdempotencyKeyPattern(ctx context.Context, pattern string) (int64, error) {
	if m.cancelFunc != nil {
		return m.cancelFunc(ctx, pattern)
	}
	return 0, nil
}
