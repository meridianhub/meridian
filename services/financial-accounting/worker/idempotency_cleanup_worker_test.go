package worker_test

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/meridianhub/meridian/services/financial-accounting/config"
	"github.com/meridianhub/meridian/services/financial-accounting/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// formatInt converts an integer to a string.
func formatInt(i int) string {
	return strconv.Itoa(i)
}

// testLogger creates a test logger with debug level.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// setupTestRedis creates a miniredis instance and returns the Redis service and cleanup function.
func setupTestRedis(t *testing.T) (*idempotency.RedisService, *redis.Client, func()) {
	t.Helper()

	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})

	redisSvc := idempotency.NewRedisService(client)

	cleanup := func() {
		_ = client.Close()
		s.Close()
	}

	return redisSvc, client, cleanup
}

// TestCleanupWorker_StaleKeyMarkedAsFailed tests that a PENDING key older than the
// threshold is marked as FAILED by the cleanup worker.
func TestCleanupWorker_StaleKeyMarkedAsFailed(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Create a PENDING key with created_at 20 minutes ago
	staleKey := idempotency.Key{
		Namespace: "test",
		Operation: "deposit",
		EntityID:  "account-123",
		RequestID: "req-stale-001",
	}

	// Mark as pending with old timestamp by directly storing via the service
	// then manipulating the created_at via raw Redis access
	err := redisSvc.MarkPending(ctx, staleKey, time.Hour)
	require.NoError(t, err)

	// Use a very short threshold for testing instead of manipulating timestamps
	// Delete the key and recreate it, then wait briefly for it to become stale
	err = redisSvc.Delete(ctx, staleKey)
	require.NoError(t, err)

	// Re-create with the normal service but use a very short stale threshold
	err = redisSvc.MarkPending(ctx, staleKey, time.Hour)
	require.NoError(t, err)

	//nolint:forbidigo // Intentional: tests time-based staleness detection; key must age before cleanup runs
	time.Sleep(50 * time.Millisecond)

	// Configure worker with very short threshold
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 10 * time.Millisecond, // Very short for testing
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	// Wait for the key to be marked as FAILED
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			result, checkErr := redisSvc.Check(ctx, staleKey)
			if checkErr != nil {
				return false
			}
			return result != nil && result.Status == idempotency.StatusFailed
		})

	require.NoError(t, err, "expected key to be marked as FAILED")

	// Verify the key status
	result, err := redisSvc.Check(ctx, staleKey)
	require.NoError(t, err)
	assert.Equal(t, idempotency.StatusFailed, result.Status)
	assert.Contains(t, result.Error, "timeout")

	// Stop worker
	w.Stop()
}

// TestCleanupWorker_FreshKeyNotMarked tests that a PENDING key younger than the
// threshold is NOT marked as FAILED.
func TestCleanupWorker_FreshKeyNotMarked(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Create a fresh PENDING key
	freshKey := idempotency.Key{
		Namespace: "test",
		Operation: "deposit",
		EntityID:  "account-456",
		RequestID: "req-fresh-001",
	}

	err := redisSvc.MarkPending(ctx, freshKey, time.Hour)
	require.NoError(t, err)

	// Configure worker with a threshold longer than our test
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 15 * time.Minute, // Key should not be stale
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	//nolint:forbidigo // Intentional: verifying fresh keys are not modified (negative assertion) requires waiting for cycles
	time.Sleep(300 * time.Millisecond)

	// Verify the key is still PENDING
	result, err := redisSvc.Check(ctx, freshKey)
	require.NoError(t, err)
	assert.Equal(t, idempotency.StatusPending, result.Status)

	// Stop worker
	w.Stop()
}

// TestCleanupWorker_BatchProcessing tests that the worker correctly processes
// multiple stale keys in batches.
func TestCleanupWorker_BatchProcessing(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Create 150 PENDING keys
	numKeys := 150
	keys := make([]idempotency.Key, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = idempotency.Key{
			Namespace: "test",
			Operation: "batch-test",
			EntityID:  "account-" + formatInt(i),
			RequestID: "req-batch-" + formatInt(i),
		}
		err := redisSvc.MarkPending(ctx, keys[i], time.Hour)
		require.NoError(t, err)
	}

	//nolint:forbidigo // Intentional: keys must age past the staleness threshold before cleanup can detect them
	time.Sleep(50 * time.Millisecond)

	// Configure worker with batch size of 100 (so we need 2 batches)
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 10 * time.Millisecond, // Very short for testing
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	// Wait for all keys to be processed
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			failedCount := 0
			for _, key := range keys {
				result, checkErr := redisSvc.Check(ctx, key)
				if checkErr == nil && result != nil && result.Status == idempotency.StatusFailed {
					failedCount++
				}
			}
			return failedCount == numKeys
		})

	require.NoError(t, err, "expected all keys to be marked as FAILED")

	// Stop worker
	w.Stop()
}

// TestCleanupWorker_GracefulShutdown tests that the worker stops cleanly without
// data loss when interrupted mid-batch.
func TestCleanupWorker_GracefulShutdown(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Create some PENDING keys
	numKeys := 10
	for i := 0; i < numKeys; i++ {
		key := idempotency.Key{
			Namespace: "test",
			Operation: "shutdown-test",
			EntityID:  "account-shutdown-" + formatInt(i),
			RequestID: "req-shutdown-" + formatInt(i),
		}
		err := redisSvc.MarkPending(ctx, key, time.Hour)
		require.NoError(t, err)
	}

	//nolint:forbidigo // Intentional: keys must age past the staleness threshold before cleanup can detect them
	time.Sleep(50 * time.Millisecond)

	// Configure worker with short intervals
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 10 * time.Millisecond,
		RunInterval:    50 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	w, err := worker.NewIdempotencyCleanupWorker(redisSvc, cfg, logger)
	require.NoError(t, err)

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)

	startedChan := make(chan struct{})
	go func() {
		close(startedChan)
		_ = w.Start(workerCtx)
	}()

	// Wait for worker to start
	<-startedChan

	//nolint:forbidigo // Intentional: allows worker to run a few cycles before testing graceful shutdown
	time.Sleep(100 * time.Millisecond)

	// Stop the worker gracefully via context cancellation
	workerCancel()

	// Stop should complete quickly without hanging
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good - shutdown completed
	case <-time.After(5 * time.Second):
		t.Fatal("worker shutdown timed out")
	}
}

// TestCleanupWorker_ValidationErrors tests that the worker constructor returns
// errors for invalid configuration.
func TestCleanupWorker_ValidationErrors(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	logger := testLogger()

	tests := []struct {
		name        string
		cleaner     idempotency.Cleaner
		cfg         config.IdempotencyCleanupConfig
		logger      *slog.Logger
		expectedErr error
	}{
		{
			name:    "nil cleaner",
			cleaner: nil,
			cfg: config.IdempotencyCleanupConfig{
				StaleThreshold: 15 * time.Minute,
				RunInterval:    5 * time.Minute,
				BatchSize:      100,
			},
			logger:      logger,
			expectedErr: worker.ErrNilCleaner,
		},
		{
			name:    "nil logger",
			cleaner: redisSvc,
			cfg: config.IdempotencyCleanupConfig{
				StaleThreshold: 15 * time.Minute,
				RunInterval:    5 * time.Minute,
				BatchSize:      100,
			},
			logger:      nil,
			expectedErr: worker.ErrNilLogger,
		},
		{
			name:    "zero run interval",
			cleaner: redisSvc,
			cfg: config.IdempotencyCleanupConfig{
				StaleThreshold: 15 * time.Minute,
				RunInterval:    0,
				BatchSize:      100,
			},
			logger:      logger,
			expectedErr: worker.ErrInvalidInterval,
		},
		{
			name:    "zero stale threshold",
			cleaner: redisSvc,
			cfg: config.IdempotencyCleanupConfig{
				StaleThreshold: 0,
				RunInterval:    5 * time.Minute,
				BatchSize:      100,
			},
			logger:      logger,
			expectedErr: worker.ErrInvalidThreshold,
		},
		{
			name:    "zero batch size",
			cleaner: redisSvc,
			cfg: config.IdempotencyCleanupConfig{
				StaleThreshold: 15 * time.Minute,
				RunInterval:    5 * time.Minute,
				BatchSize:      0,
			},
			logger:      logger,
			expectedErr: worker.ErrInvalidBatchSize,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := worker.NewIdempotencyCleanupWorker(tc.cleaner, tc.cfg, tc.logger)
			assert.ErrorIs(t, err, tc.expectedErr)
		})
	}
}

// TestCleanupWorker_MetricsRecorded tests that the cleanup worker correctly
// records Prometheus metrics when processing stale keys.
func TestCleanupWorker_MetricsRecorded(t *testing.T) {
	redisSvc, _, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	logger := testLogger()

	// Get initial cleanup counter value
	initialCleanedUp := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("metrics-test"),
	)

	// Create some PENDING keys with a unique namespace for metrics isolation
	numKeys := 5
	keys := make([]idempotency.Key, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = idempotency.Key{
			Namespace: "metrics-test",
			Operation: "payment",
			EntityID:  "account-metrics-" + formatInt(i),
			RequestID: "req-metrics-" + formatInt(i),
		}
		err := redisSvc.MarkPending(ctx, keys[i], time.Hour)
		require.NoError(t, err)
	}

	//nolint:forbidigo // Intentional: keys must age past the staleness threshold before cleanup can detect them
	time.Sleep(50 * time.Millisecond)

	// Configure worker with short threshold
	cfg := config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 10 * time.Millisecond,
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}

	// Create worker with metrics collector
	metrics := idempotency.NewMetricsCollector("cleanup-worker-test")
	w, err := worker.NewIdempotencyCleanupWorkerWithMetrics(redisSvc, cfg, logger, metrics)
	require.NoError(t, err)

	// Start worker in background
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	go func() {
		_ = w.Start(workerCtx)
	}()

	// Wait for all keys to be processed
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			failedCount := 0
			for _, key := range keys {
				result, checkErr := redisSvc.Check(ctx, key)
				if checkErr == nil && result != nil && result.Status == idempotency.StatusFailed {
					failedCount++
				}
			}
			return failedCount == numKeys
		})

	require.NoError(t, err, "expected all keys to be marked as FAILED")

	// Verify cleanup counter was incremented for the namespace
	newCleanedUp := testutil.ToFloat64(
		idempotency.ExposeMetricsForTesting.KeysCleanedUpTotal.WithLabelValues("metrics-test"),
	)

	// The cleanup counter should have increased by at least numKeys
	assert.GreaterOrEqual(t, newCleanedUp, initialCleanedUp+float64(numKeys),
		"cleanup counter should be incremented for each processed key")

	// Stop worker
	w.Stop()
}
