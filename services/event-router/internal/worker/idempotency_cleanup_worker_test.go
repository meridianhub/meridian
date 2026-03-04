package worker_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/event-router/internal/worker"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
)

// Shared CockroachDB container for worker tests.
var (
	workerOnce sync.Once
	workerPool *pgxpool.Pool
	workerErr  error
)

func getWorkerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	workerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		container, err := cockroachdb.Run(ctx,
			"cockroachdb/cockroach:v24.3.0",
			cockroachdb.WithDatabase("worker_test_db"),
			cockroachdb.WithUser("root"),
			cockroachdb.WithInsecure(),
		)
		if err != nil {
			workerErr = err
			return
		}

		connConfig, err := container.ConnectionConfig(ctx)
		if err != nil {
			workerErr = err
			return
		}

		pool, err := pgxpool.New(ctx, connConfig.ConnString())
		if err != nil {
			workerErr = err
			return
		}
		workerPool = pool
	})

	if workerErr != nil {
		t.Fatalf("failed to initialize CockroachDB for worker tests: %v", workerErr)
	}
	return workerPool
}

func TestNewIdempotencyCleanupWorker_NilPool(t *testing.T) {
	cfg := worker.DefaultIdempotencyCleanupConfig()
	_, err := worker.NewIdempotencyCleanupWorker(nil, cfg, slog.Default())
	require.Error(t, err)
	assert.Equal(t, worker.ErrNilPool, err)
}

func TestNewIdempotencyCleanupWorker_NilLogger(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.DefaultIdempotencyCleanupConfig()
	_, err := worker.NewIdempotencyCleanupWorker(pool, cfg, nil)
	require.Error(t, err)
	assert.Equal(t, worker.ErrNilLogger, err)
}

func TestNewIdempotencyCleanupWorker_InvalidInterval(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.IdempotencyCleanupConfig{CleanupInterval: 0}
	_, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.Error(t, err)
	assert.Equal(t, worker.ErrInvalidInterval, err)
}

func TestNewIdempotencyCleanupWorker_ValidConfig(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.DefaultIdempotencyCleanupConfig()
	w, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.NoError(t, err)
	assert.NotNil(t, w)
}

func TestIdempotencyCleanupWorker_StartsAndStops(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.IdempotencyCleanupConfig{
		CleanupInterval: 100 * time.Millisecond,
	}
	w, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	// Wait until the worker has set running=true before stopping.
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(w.Running))

	w.Stop()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop within timeout")
	}
}

func TestIdempotencyCleanupWorker_ContextCancellation_Stops(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.IdempotencyCleanupConfig{
		CleanupInterval: 100 * time.Millisecond,
	}
	w, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	// Wait until the worker has set running=true before cancelling.
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(w.Running))
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestIdempotencyCleanupWorker_StopIdempotent(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.DefaultIdempotencyCleanupConfig()
	w, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.NoError(t, err)

	// Stop before starting should not panic
	w.Stop()
	w.Stop() // second call should not panic
}

func TestIdempotencyCleanupWorker_AlreadyRunning_ReturnsError(t *testing.T) {
	pool := getWorkerPool(t)
	cfg := worker.IdempotencyCleanupConfig{
		CleanupInterval: 100 * time.Millisecond,
	}
	w, err := worker.NewIdempotencyCleanupWorker(pool, cfg, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Stop()

	go func() {
		_ = w.Start(ctx)
	}()

	// Wait until the worker has set running=true, then verify a second Start returns ErrAlreadyRunning.
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(w.Running))

	err = w.Start(ctx)
	require.ErrorIs(t, err, worker.ErrAlreadyRunning)
}
