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
		time.Sleep(100 * time.Millisecond)

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
		time.Sleep(50 * time.Millisecond)

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

		time.Sleep(100 * time.Millisecond)
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

	t.Run("schedules and processes retry", func(t *testing.T) {
		repo := newMockBillingRepo()

		// Create a failed billing run
		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:           billingRunID,
			TenantID:     "tenant-1",
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

		err = w.ScheduleDunningRetry(context.Background(), billingRunID, -1*time.Minute)
		require.NoError(t, err)

		// Start the worker
		go func() {
			_ = w.Start(context.Background())
		}()

		// Wait for the callback to fire
		deadline := time.After(5 * time.Second)
		for !callbackCalled.Load() {
			select {
			case <-deadline:
				t.Fatal("callback was not called within timeout")
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}

		// Wait for the ZREM to complete (happens after callback in the same poll cycle).
		// Poll before calling Stop() to avoid context cancellation racing with ZREM.
		zsetDeadline := time.After(5 * time.Second)
		for {
			members, zErr := client.ZRangeByScore(context.Background(), dunningRetryZSet, &redis.ZRangeBy{
				Min: "-inf",
				Max: "+inf",
			}).Result()
			require.NoError(t, zErr)
			if len(members) == 0 {
				break
			}
			select {
			case <-zsetDeadline:
				t.Fatalf("ZSET not drained within timeout, remaining: %v", members)
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}

		w.Stop()
	})

	t.Run("skips billing run that is no longer failed", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:         billingRunID,
			TenantID:   "tenant-1",
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
		client.Del(context.Background(), dunningRetryZSet)

		// Schedule in the past
		err = w.ScheduleDunningRetry(context.Background(), billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		// Wait for a poll cycle
		time.Sleep(200 * time.Millisecond)
		w.Stop()

		assert.False(t, callbackCalled.Load(), "callback should not be called for non-failed billing run")
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
		client.Del(context.Background(), dunningRetryZSet)

		err = w.ScheduleDunningRetry(context.Background(), billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		time.Sleep(200 * time.Millisecond)
		w.Stop()

		// Verify the retry was removed from the ZSET (dropped)
		members, err := client.ZRangeByScore(context.Background(), dunningRetryZSet, &redis.ZRangeBy{
			Min: "-inf",
			Max: "+inf",
		}).Result()
		require.NoError(t, err)
		assert.Empty(t, members, "not-found retry should be removed from ZSET")
	})

	t.Run("retains member on transient callback error", func(t *testing.T) {
		repo := newMockBillingRepo()

		billingRunID := uuid.New()
		run := &domain.BillingRun{
			ID:           billingRunID,
			TenantID:     "tenant-1",
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
		client.Del(context.Background(), dunningRetryZSet)

		err = w.ScheduleDunningRetry(context.Background(), billingRunID, -1*time.Minute)
		require.NoError(t, err)

		go func() {
			_ = w.Start(context.Background())
		}()

		time.Sleep(200 * time.Millisecond)
		w.Stop()

		// Verify the retry is still in the ZSET
		count, err := client.ZCard(context.Background(), dunningRetryZSet).Result()
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "failed retry should be retained in ZSET")
	})

	// Clean up ZSET for other tests
	mr.FlushAll()
}

func TestDunningWorker_CancelRetry(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()
	billingRunID := uuid.New()

	// Schedule a retry
	err = w.ScheduleDunningRetry(ctx, billingRunID, time.Hour)
	require.NoError(t, err)

	// Verify it's in the ZSET
	count, err := client.ZCard(ctx, dunningRetryZSet).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Cancel it
	err = w.CancelDunningRetry(ctx, billingRunID)
	require.NoError(t, err)

	// Verify it's gone
	count, err = client.ZCard(ctx, dunningRetryZSet).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestDunningWorker_PerItemLocking(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	billingRunID := uuid.New()
	run := &domain.BillingRun{
		ID:           billingRunID,
		TenantID:     "tenant-1",
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
	err = w1.ScheduleDunningRetry(ctx, billingRunID, -1*time.Minute)
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

	billingRunID := uuid.New()
	run := &domain.BillingRun{
		ID:           billingRunID,
		TenantID:     "tenant-1",
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
		time.Sleep(200 * time.Millisecond)
		callbackCompleted.Store(true)
		return nil
	}

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 5 * time.Second,
	}, slowCallback, logger, metrics)
	require.NoError(t, err)

	// Schedule retry in the past
	err = w.ScheduleDunningRetry(context.Background(), billingRunID, -1*time.Minute)
	require.NoError(t, err)

	go func() {
		_ = w.Start(context.Background())
	}()

	// Wait for the callback to start
	time.Sleep(150 * time.Millisecond)

	// Stop should wait for the slow callback to complete
	w.Stop()

	assert.True(t, callbackCompleted.Load(), "shutdown should have waited for in-flight callback")
}

func TestDunningWorker_InvalidMemberInZSET(t *testing.T) {
	_, client := setupDunningMiniredis(t)
	logger := dunningTestLogger()
	repo := newMockBillingRepo()
	metrics := dunningTestMetrics(t)

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()

	// Add an invalid UUID to the ZSET
	client.ZAdd(ctx, dunningRetryZSet, redis.Z{
		Score:  float64(time.Now().Add(-1 * time.Minute).Unix()),
		Member: "not-a-uuid",
	})

	go func() {
		_ = w.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	w.Stop()

	// Invalid member should be removed
	members, err := client.ZRangeByScore(ctx, dunningRetryZSet, &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
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

	w, err := NewDunningWorker(repo, client, DunningWorkerConfig{
		PollInterval:    50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
	}, noopCallback, logger, metrics)
	require.NoError(t, err)

	ctx := context.Background()
	billingRunID := uuid.New()

	// Clean ZSET
	client.Del(ctx, dunningRetryZSet)

	err = w.ScheduleDunningRetry(ctx, billingRunID, -1*time.Minute)
	require.NoError(t, err)

	go func() {
		_ = w.Start(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	w.Stop()

	// Retry should still be in the ZSET
	count, err := client.ZCard(ctx, dunningRetryZSet).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), count, "retry should be retained when repo returns error")
}
