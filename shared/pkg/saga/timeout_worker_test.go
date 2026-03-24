package saga

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTimeoutWorkerConfig(t *testing.T) {
	cfg := DefaultTimeoutWorkerConfig()
	assert.Equal(t, 1*time.Minute, cfg.PollInterval)
	assert.Equal(t, 100, cfg.BatchSize)
}

func TestNewTimeoutWorker_nil_config_uses_defaults(t *testing.T) {
	worker := NewTimeoutWorker(nil, nil)
	assert.Equal(t, 1*time.Minute, worker.config.PollInterval)
	assert.Equal(t, 100, worker.config.BatchSize)
}

func TestNewTimeoutWorker_custom_config(t *testing.T) {
	cfg := &TimeoutWorkerConfig{
		PollInterval: 30 * time.Second,
		BatchSize:    50,
	}
	worker := NewTimeoutWorker(nil, cfg)
	assert.Equal(t, 30*time.Second, worker.config.PollInterval)
	assert.Equal(t, 50, worker.config.BatchSize)
}

func TestNewTimeoutWorker_invalid_poll_interval_uses_default(t *testing.T) {
	cfg := &TimeoutWorkerConfig{
		PollInterval: -1 * time.Second,
		BatchSize:    50,
	}
	worker := NewTimeoutWorker(nil, cfg)
	assert.Equal(t, 1*time.Minute, worker.config.PollInterval)
	assert.Equal(t, 50, worker.config.BatchSize)
}

func TestNewTimeoutWorker_zero_poll_interval_uses_default(t *testing.T) {
	cfg := &TimeoutWorkerConfig{
		PollInterval: 0,
		BatchSize:    50,
	}
	worker := NewTimeoutWorker(nil, cfg)
	assert.Equal(t, 1*time.Minute, worker.config.PollInterval)
}

func TestNewTimeoutWorker_invalid_batch_size_uses_default(t *testing.T) {
	cfg := &TimeoutWorkerConfig{
		PollInterval: 30 * time.Second,
		BatchSize:    -1,
	}
	worker := NewTimeoutWorker(nil, cfg)
	assert.Equal(t, 100, worker.config.BatchSize)
}

func TestNewTimeoutWorker_zero_batch_size_uses_default(t *testing.T) {
	cfg := &TimeoutWorkerConfig{
		PollInterval: 30 * time.Second,
		BatchSize:    0,
	}
	worker := NewTimeoutWorker(nil, cfg)
	assert.Equal(t, 100, worker.config.BatchSize)
}

func TestTimeoutWorker_WithLogger(t *testing.T) {
	logger := slog.Default()
	worker := NewTimeoutWorker(nil, nil).WithLogger(logger)
	assert.Equal(t, logger, worker.logger)
}

func TestTimeoutWorker_Start_exits_on_cancel(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	cfg := &TimeoutWorkerConfig{
		PollInterval: 100 * time.Millisecond,
		BatchSize:    10,
	}

	worker := NewTimeoutWorker(db, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- worker.Start(ctx)
	}()

	// Give it a moment to start, then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(30 * time.Second):
		t.Fatal("Start did not exit after context cancellation")
	}
}
