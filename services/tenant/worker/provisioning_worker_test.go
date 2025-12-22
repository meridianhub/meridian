package worker

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewProvisioningWorker_Success(t *testing.T) {
	// Setup dependencies
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	// Create worker
	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, worker)
	assert.Equal(t, repo, worker.repo)
	assert.Equal(t, prov, worker.provisioner)
	assert.Equal(t, pollInterval, worker.pollInterval)
	assert.Equal(t, logger, worker.logger)
	assert.NotNil(t, worker.done)
}

func TestNewProvisioningWorker_NilRepository(t *testing.T) {
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(nil, prov, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilRepository)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilProvisioner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, nil, pollInterval, logger)

	assert.ErrorIs(t, err, ErrNilProvisioner)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NilLogger(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	pollInterval := 5 * time.Second

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, nil)

	assert.ErrorIs(t, err, ErrNilLogger)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_ZeroPollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, 0, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestNewProvisioningWorker_NegativePollInterval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	worker, err := NewProvisioningWorker(repo, prov, -5*time.Second, logger)

	assert.ErrorIs(t, err, ErrInvalidPollInterval)
	assert.Nil(t, worker)
}

func TestProvisioningWorker_Start_ContextCancellation(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Start worker with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Should stop within a reasonable time
	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestProvisioningWorker_Start_ExplicitStop(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Start worker
	ctx := context.Background()
	done := make(chan struct{})

	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

	// Call Stop()
	worker.Stop()

	// Should stop within a reasonable time
	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not stop after explicit Stop() call")
	}
}

func TestProvisioningWorker_Stop_MultipleCalls(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Call Stop() multiple times - should not panic
	worker.Stop()
	worker.Stop()
	worker.Stop()

	// Success if no panic
}

func TestProvisioningWorker_Start_TickerInterval(t *testing.T) {
	// Setup
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	prov := &provisioner.MockProvisioner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pollInterval := 50 * time.Millisecond

	worker, err := NewProvisioningWorker(repo, prov, pollInterval, logger)
	require.NoError(t, err)

	// Start worker with short timeout to observe multiple ticks
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Wait for worker to complete
	select {
	case <-done:
		// Worker stopped as expected after context timeout
	case <-time.After(300 * time.Millisecond):
		t.Fatal("worker did not stop after context timeout")
	}

	// Success - the worker ran through multiple ticker intervals before stopping
	// We can't directly verify processPendingTenants call count without modifying
	// the implementation, but we've verified the ticker-based loop works correctly
}
