package saga

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrphanWatcherPeriodicScan verifies that the watcher performs periodic scans.
func TestOrphanWatcherPeriodicScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Track scan invocations
	var scanCount atomic.Int32

	config := NewClaimConfig()
	config.MaxJitterMS = 0 // Disable jitter for deterministic tests
	config.PodID = "test-periodic-pod"

	// Create watcher with short scan interval
	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithOrphanScanCallback(func() {
			scanCount.Add(1)
		}),
		WithScanInterval(200*time.Millisecond), // Short interval for testing
	)

	// Start the watcher
	watcher.Start(ctx)
	defer watcher.Stop()

	// Wait for periodic scans to occur (should be at least initial + 2 periodic)
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return scanCount.Load() >= 3 // Initial + at least 2 periodic scans
		})
	require.NoError(t, err, "expected periodic scans to occur")
	t.Logf("Scan count: %d", scanCount.Load())
}

// TestOrphanWatcherClaimsOrphanedSagas verifies the watcher claims orphaned sagas.
func TestOrphanWatcherClaimsOrphanedSagas(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := NewClaimConfig()
	config.MaxJitterMS = 0
	config.PodID = "claimer-pod"

	var scanCompleted atomic.Int32

	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithOrphanScanCallback(func() {
			scanCompleted.Add(1)
		}),
		WithScanInterval(200*time.Millisecond),
	)

	// Create orphaned sagas before starting watcher
	for i := 0; i < 5; i++ {
		createTestSaga(t, db, SagaStatusRunning, nil, nil)
	}

	watcher.Start(ctx)
	defer watcher.Stop()

	// Wait for sagas to be claimed
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			var claimed int64
			db.Model(&SagaInstance{}).
				Where("claimed_by_pod = ?", config.PodID).
				Count(&claimed)
			return claimed == 5
		})
	require.NoError(t, err, "expected all 5 sagas to be claimed")

	// Verify all sagas are claimed by our pod
	var claimedSagas []SagaInstance
	err = db.Where("claimed_by_pod = ?", config.PodID).Find(&claimedSagas).Error
	require.NoError(t, err)
	assert.Len(t, claimedSagas, 5, "all 5 sagas should be claimed")
}

// TestOrphanWatcherClaimsConcurrentOrphans verifies the watcher handles
// multiple orphaned sagas correctly.
func TestOrphanWatcherClaimsConcurrentOrphans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var scanCount atomic.Int32

	config := NewClaimConfig()
	config.PodID = "concurrent-test-pod"
	config.MaxJitterMS = 0
	config.BatchSize = 50 // Larger batch for concurrent test

	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithOrphanScanCallback(func() {
			scanCount.Add(1)
		}),
		WithScanInterval(100*time.Millisecond),
	)

	// Create 100 orphaned sagas (no claimed_by_pod)
	for i := 0; i < 100; i++ {
		createTestSaga(t, db, SagaStatusRunning, nil, nil)
	}

	watcher.Start(ctx)
	defer watcher.Stop()

	// Wait for all sagas to be claimed
	err = await.New().
		AtMost(30 * time.Second).
		PollInterval(200 * time.Millisecond).
		Until(func() bool {
			var claimed int64
			db.Model(&SagaInstance{}).
				Where("claimed_by_pod = ?", config.PodID).
				Count(&claimed)
			return claimed == 100
		})
	require.NoError(t, err, "expected all 100 sagas to be claimed")

	t.Logf("Total scans performed: %d", scanCount.Load())
}

// TestOrphanWatcherGracefulShutdown verifies the watcher shuts down cleanly.
func TestOrphanWatcherGracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	watcher := NewOrphanWatcher(
		db,
		NewClaimConfig(),
		slog.Default(),
	)

	// Start and immediately stop
	watcher.Start(ctx)

	// Stop should complete quickly
	done := make(chan struct{})
	go func() {
		watcher.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not shut down within timeout")
	}
}

// TestOrphanWatcherIdempotentStartStop verifies Start/Stop are idempotent.
func TestOrphanWatcherIdempotentStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	watcher := NewOrphanWatcher(
		db,
		NewClaimConfig(),
		slog.Default(),
	)

	// Start multiple times - should not panic or create multiple goroutines
	watcher.Start(ctx)
	watcher.Start(ctx) // Second start should be no-op
	watcher.Start(ctx) // Third start should be no-op

	// Stop multiple times - should not panic
	watcher.Stop()
	watcher.Stop() // Second stop should block briefly then return
	watcher.Stop() // Third stop should block briefly then return
}

// TestOrphanWatcherClaimsExpiredLeases verifies the watcher claims sagas
// with expired leases (simulating pod crash scenario).
func TestOrphanWatcherClaimsExpiredLeases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	config := NewClaimConfig()
	config.MaxJitterMS = 0
	config.PodID = "new-claimer-pod"

	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithScanInterval(200*time.Millisecond),
	)

	// Create sagas with expired leases (simulating crashed pod)
	now := time.Now()
	expiredLease := now.Add(-10 * time.Minute)
	deadPod := "dead-pod"

	for i := 0; i < 5; i++ {
		saga := &SagaInstance{
			ID:               uuid.New(),
			SagaDefinitionID: uuid.New(),
			PartyID:          uuid.New(),
			CorrelationID:    uuid.New(),
			Status:           SagaStatusRunning,
			ClaimedByPod:     &deadPod,
			ClaimedAt:        &now,
			LeaseExpiresAt:   &expiredLease, // Lease already expired
		}
		require.NoError(t, db.Create(saga).Error)
	}

	watcher.Start(ctx)
	defer watcher.Stop()

	// Wait for sagas to be claimed by the new pod
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			var claimed int64
			db.Model(&SagaInstance{}).
				Where("claimed_by_pod = ?", config.PodID).
				Count(&claimed)
			return claimed == 5
		})
	require.NoError(t, err, "expected all 5 sagas with expired leases to be claimed")
}
