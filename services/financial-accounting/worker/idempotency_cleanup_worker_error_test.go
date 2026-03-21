package worker_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/financial-accounting/config"
	"github.com/meridianhub/meridian/services/financial-accounting/worker"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errMockScan is returned by the mock scanner to simulate scan failures.
var errMockScan = errors.New("redis scan failed")

// errMockMark is returned by the mock marker to simulate mark failures.
var errMockMark = errors.New("redis mark failed")

func errorTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func defaultTestConfig() config.IdempotencyCleanupConfig {
	return config.IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 1 * time.Millisecond,
		RunInterval:    100 * time.Millisecond,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}
}

// TestCleanupWorker_ScanError tests that the worker handles scan failures gracefully.
// This covers the runCleanupIteration scan error path.
func TestCleanupWorker_ScanError(t *testing.T) {
	var callCount atomic.Int32
	cleaner := &mockCleanerFunc{
		scanFn: func(_ context.Context, _ string, _ time.Duration, _ int) ([]idempotency.StalePendingKey, error) {
			callCount.Add(1)
			return nil, errMockScan
		},
		markFn: func(_ context.Context, _ idempotency.StalePendingKey, _ string) error {
			return nil
		},
	}

	w, err := worker.NewIdempotencyCleanupWorker(cleaner, defaultTestConfig(), errorTestLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	// Wait for at least one iteration to run and hit the scan error
	waitErr := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return callCount.Load() >= 1
		})
	require.NoError(t, waitErr)
	cancel()

	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop in time")
	}
}

// TestCleanupWorker_MarkStaleAsFailedError tests that the worker handles
// MarkStaleAsFailed errors gracefully and counts them as failures.
// This covers the processStaleKey error path and the logIterationComplete failure branch.
func TestCleanupWorker_MarkStaleAsFailedError(t *testing.T) {
	// Set up a key that will be found by scan but fail to be marked
	staleKey := idempotency.StalePendingKey{
		RedisKey: "idempotency:result:test:op:entity",
		Result: &idempotency.Result{
			Key: idempotency.Key{
				Namespace: "test-service",
				Operation: "test-op",
				EntityID:  "entity-1",
				RequestID: "req-1",
			},
			Status:    idempotency.StatusPending,
			CreatedAt: time.Now().Add(-10 * time.Minute),
		},
		Age: 10 * time.Minute,
	}

	// First scan returns one stale key, subsequent scans return empty
	var callCount atomic.Int32
	cleaner := &mockCleanerFunc{
		scanFn: func(_ context.Context, _ string, _ time.Duration, _ int) ([]idempotency.StalePendingKey, error) {
			n := callCount.Add(1)
			if n == 1 {
				return []idempotency.StalePendingKey{staleKey}, nil
			}
			return nil, nil
		},
		markFn: func(_ context.Context, _ idempotency.StalePendingKey, _ string) error {
			return errMockMark
		},
	}

	w, err := worker.NewIdempotencyCleanupWorker(cleaner, defaultTestConfig(), errorTestLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Start(ctx)
	}()

	// Wait for at least one iteration to complete
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return callCount.Load() >= 1
		})
	require.NoError(t, err)

	cancel()
}

// TestCleanupWorker_GetServiceFromKey_EmptyNamespace tests the getServiceFromKey fallback
// when the stale key has a Result with an empty Namespace.
func TestCleanupWorker_GetServiceFromKey_EmptyNamespace(t *testing.T) {
	staleKeyEmptyNS := idempotency.StalePendingKey{
		RedisKey: "idempotency:result:test",
		Result: &idempotency.Result{
			Key: idempotency.Key{
				Namespace: "", // Empty namespace
				Operation: "test-op",
				EntityID:  "entity-2",
				RequestID: "req-2",
			},
			Status:    idempotency.StatusPending,
			CreatedAt: time.Now().Add(-10 * time.Minute),
		},
		Age: 10 * time.Minute,
	}

	var callCount atomic.Int32
	cleaner := &mockCleanerFunc{
		scanFn: func(_ context.Context, _ string, _ time.Duration, _ int) ([]idempotency.StalePendingKey, error) {
			n := callCount.Add(1)
			if n == 1 {
				return []idempotency.StalePendingKey{staleKeyEmptyNS}, nil
			}
			return nil, nil
		},
		markFn: func(_ context.Context, _ idempotency.StalePendingKey, _ string) error {
			return nil
		},
	}

	w, err := worker.NewIdempotencyCleanupWorker(cleaner, defaultTestConfig(), errorTestLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Start(ctx)
	}()

	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return callCount.Load() >= 1
		})
	require.NoError(t, err)

	cancel()
}

// mockCleanerFunc is a function-based mock for more flexible testing.
type mockCleanerFunc struct {
	scanFn func(ctx context.Context, pattern string, threshold time.Duration, limit int) ([]idempotency.StalePendingKey, error)
	markFn func(ctx context.Context, key idempotency.StalePendingKey, reason string) error
}

func (m *mockCleanerFunc) ScanStalePendingKeys(ctx context.Context, pattern string, threshold time.Duration, limit int) ([]idempotency.StalePendingKey, error) {
	return m.scanFn(ctx, pattern, threshold, limit)
}

func (m *mockCleanerFunc) MarkStaleAsFailed(ctx context.Context, key idempotency.StalePendingKey, reason string) error {
	return m.markFn(ctx, key, reason)
}

// Ensure mockCleanerFunc implements idempotency.Cleaner.
var _ idempotency.Cleaner = (*mockCleanerFunc)(nil)
