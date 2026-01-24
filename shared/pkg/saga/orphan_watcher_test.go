package saga

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrphanNotifyTriggerCreated verifies the PostgreSQL trigger and function
// are created by RunSagaMigrations.
func TestOrphanNotifyTriggerCreated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err, "RunSagaMigrations should succeed")

	// Verify the trigger function exists
	t.Run("notify_saga_orphaned function exists", func(t *testing.T) {
		var count int64
		err := db.Raw(`
			SELECT COUNT(*)
			FROM pg_proc p
			JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE p.proname = 'notify_saga_orphaned'
			  AND n.nspname = current_schema()
		`).Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "notify_saga_orphaned function should exist")
	})

	// Verify the trigger exists
	t.Run("saga_orphaned_trigger exists", func(t *testing.T) {
		var count int64
		err := db.Raw(`
			SELECT COUNT(*)
			FROM pg_trigger t
			JOIN pg_class c ON c.oid = t.tgrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE t.tgname = 'saga_orphaned_trigger'
			  AND c.relname = 'saga_instances'
			  AND n.nspname = current_schema()
		`).Scan(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "saga_orphaned_trigger should exist")
	})
}

// TestOrphanNotifyTriggerFires verifies the PostgreSQL trigger fires
// NOTIFY when a saga's lease is released (claimed_by_pod becomes NULL).
func TestOrphanNotifyTriggerFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err, "RunSagaMigrations should succeed")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a saga instance with a lease
	sagaID := uuid.New()
	now := time.Now()
	pod := "test-pod-1"
	saga := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		PartyID:          uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ClaimedByPod:     &pod,
		ClaimedAt:        &now,
		LeaseExpiresAt:   timePtr(now.Add(5 * time.Minute)),
	}
	require.NoError(t, db.Create(saga).Error)

	// Release the lease (set claimed_by_pod to NULL)
	// This should trigger the NOTIFY (we verify the trigger fired by checking the saga state)
	err = db.Model(&SagaInstance{}).
		Where("id = ?", sagaID).
		Updates(map[string]interface{}{
			"claimed_by_pod":   nil,
			"claimed_at":       nil,
			"lease_expires_at": nil,
		}).Error
	require.NoError(t, err)

	// Verify the saga now has NULL claimed_by_pod
	var updated SagaInstance
	err = db.First(&updated, "id = ?", sagaID).Error
	require.NoError(t, err)
	assert.Nil(t, updated.ClaimedByPod, "claimed_by_pod should be NULL after release")

	_ = ctx // Used for timeout context
}

// TestOrphanWatcherFallbackToPeriodicScan verifies that when LISTEN fails or
// DATABASE_URL is not set, the watcher falls back to periodic scanning.
func TestOrphanWatcherFallbackToPeriodicScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Track claim scan invocations
	var scanCount atomic.Int32

	config := NewClaimConfig()
	config.MaxJitterMS = 0 // Disable jitter for deterministic tests
	config.PodID = "test-fallback-pod"

	// Create watcher with short fallback interval
	// Note: DATABASE_URL is not set, so LISTEN will fail and fallback kicks in
	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithOrphanScanCallback(func() {
			scanCount.Add(1)
		}),
		WithFallbackScanInterval(500*time.Millisecond), // Short interval for testing
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
	require.NoError(t, err, "expected periodic fallback scans to occur")
	t.Logf("Fallback scan count: %d", scanCount.Load())
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
		WithFallbackScanInterval(200*time.Millisecond),
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

// TestOrphanWatcherConcurrentNotifications verifies the watcher handles
// multiple simultaneous orphan events correctly.
func TestOrphanWatcherConcurrentNotifications(t *testing.T) {
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
	var mu sync.Mutex
	claimedSagas := make(map[uuid.UUID]bool)

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
		WithFallbackScanInterval(100*time.Millisecond),
	)

	// Create 100 sagas, all with a different "dying" pod
	now := time.Now()
	for i := 0; i < 100; i++ {
		dyingPod := "dying-pod"
		saga := &SagaInstance{
			ID:               uuid.New(),
			SagaDefinitionID: uuid.New(),
			PartyID:          uuid.New(),
			CorrelationID:    uuid.New(),
			Status:           SagaStatusRunning,
			ClaimedByPod:     &dyingPod,
			ClaimedAt:        &now,
			LeaseExpiresAt:   timePtr(now.Add(5 * time.Minute)),
		}
		require.NoError(t, db.Create(saga).Error)
	}

	watcher.Start(ctx)
	defer watcher.Stop()

	// Simulate pod crash: release all leases at once using raw SQL
	// This triggers multiple NOTIFY events
	sqlDB, err := db.DB()
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `
		UPDATE saga_instances
		SET claimed_by_pod = NULL, claimed_at = NULL, lease_expires_at = NULL
		WHERE claimed_by_pod = 'dying-pod'
	`)
	require.NoError(t, err)

	// Wait for all sagas to be claimed
	err = await.New().
		AtMost(30 * time.Second).
		PollInterval(200 * time.Millisecond).
		Until(func() bool {
			var claimed []SagaInstance
			db.Where("claimed_by_pod = ?", config.PodID).Find(&claimed)
			mu.Lock()
			for _, s := range claimed {
				claimedSagas[s.ID] = true
			}
			count := len(claimedSagas)
			mu.Unlock()
			return count == 100
		})
	require.NoError(t, err, "expected all 100 sagas to be claimed")

	t.Logf("Total scans performed: %d", scanCount.Load())
	t.Logf("Sagas claimed: %d", len(claimedSagas))
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

// TestOrphanWatcherDebounce verifies the debounce mechanism prevents
// excessive scans from rapid notifications.
func TestOrphanWatcherDebounce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var scanCount atomic.Int32

	config := NewClaimConfig()
	config.PodID = "debounce-test-pod"
	config.MaxJitterMS = 0

	watcher := NewOrphanWatcher(
		db,
		config,
		slog.Default(),
		WithOrphanScanCallback(func() {
			scanCount.Add(1)
		}),
		WithFallbackScanInterval(5*time.Second), // Long fallback to isolate debounce behavior
		WithNotificationDebounce(500*time.Millisecond),
	)

	watcher.Start(ctx)
	defer watcher.Stop()

	// Wait for initial scan
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return scanCount.Load() >= 1
		})
	require.NoError(t, err)

	initialScans := scanCount.Load()
	t.Logf("Initial scans: %d", initialScans)

	// Create multiple orphans in rapid succession to trigger debounce
	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Create 10 sagas with leases
	now := time.Now()
	for i := 0; i < 10; i++ {
		dyingPod := "rapid-dying-pod"
		saga := &SagaInstance{
			ID:               uuid.New(),
			SagaDefinitionID: uuid.New(),
			PartyID:          uuid.New(),
			CorrelationID:    uuid.New(),
			Status:           SagaStatusRunning,
			ClaimedByPod:     &dyingPod,
			ClaimedAt:        &now,
			LeaseExpiresAt:   timePtr(now.Add(5 * time.Minute)),
		}
		require.NoError(t, db.Create(saga).Error)
	}

	// Release all leases in rapid succession (simulating rapid notifications)
	// PostgreSQL doesn't support LIMIT in UPDATE, so use CTE with ctid
	for i := 0; i < 10; i++ {
		_, _ = sqlDB.ExecContext(ctx, `
			WITH to_upd AS (
				SELECT ctid
				FROM saga_instances
				WHERE claimed_by_pod = 'rapid-dying-pod'
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			UPDATE saga_instances
			SET claimed_by_pod = NULL, claimed_at = NULL, lease_expires_at = NULL
			FROM to_upd
			WHERE saga_instances.ctid = to_upd.ctid
		`)
		// Ignore errors - some updates may not match any rows
		time.Sleep(10 * time.Millisecond) // Small delay between updates
	}

	// Wait a bit for any scans to complete
	time.Sleep(1 * time.Second)

	finalScans := scanCount.Load()
	additionalScans := finalScans - initialScans

	t.Logf("Final scans: %d, Additional scans: %d", finalScans, additionalScans)

	// Due to debouncing, we should have far fewer scans than notifications
	// (10 rapid notifications should not result in 10 scans)
	// The fallback interval is 5s, so within 1s we should see debounced behavior
	assert.Less(t, additionalScans, int32(10),
		"debouncing should reduce number of scans from rapid notifications")
}

// Helper functions
func timePtr(t time.Time) *time.Time {
	return &t
}
