package saga

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// createLeaseTestSaga creates a saga instance for lease renewal testing with specific pod ownership.
func createLeaseTestSaga(t *testing.T, db *gorm.DB, podID string, leaseExpiresAt time.Time) uuid.UUID {
	t.Helper()

	sagaID := uuid.New()
	now := time.Now()

	// Use raw SQL to insert with specific lease data
	err := db.Exec(`
		INSERT INTO saga_instances (id, saga_definition_id, correlation_id, status, claimed_by_pod, claimed_at, lease_expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sagaID, uuid.New(), uuid.New(), SagaStatusRunning, podID, now, leaseExpiresAt, now, now).Error

	require.NoError(t, err, "Failed to create test saga")

	return sagaID
}

// getLeaseExpiresAtForTest retrieves the lease_expires_at timestamp for a saga.
func getLeaseExpiresAtForTest(t *testing.T, db *gorm.DB, sagaID uuid.UUID) time.Time {
	t.Helper()

	var result struct {
		LeaseExpiresAt time.Time `gorm:"column:lease_expires_at"`
	}

	err := db.Table("saga_instances").Select("lease_expires_at").Where("id = ?", sagaID).Scan(&result).Error
	require.NoError(t, err, "Failed to get lease_expires_at")

	return result.LeaseExpiresAt
}

func TestLeaseRenewer_RenewsLeaseAtInterval(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-lease-1"
	initialExpiry := time.Now().Add(1 * time.Minute)
	sagaID := createLeaseTestSaga(t, db, podID, initialExpiry)

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Use fast interval for testing (100ms)
	renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(100*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	renewer.Start(ctx)
	defer renewer.Stop()

	// Wait for at least 2 renewals
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			expiry := getLeaseExpiresAtForTest(t, db, sagaID)
			// Lease should be extended to ~5 minutes in future (with some tolerance)
			return expiry.After(time.Now().Add(4 * time.Minute))
		})
	require.NoError(t, err, "Lease should be renewed")

	// Verify multiple renewals occurred by waiting for a strictly later expiry
	firstExpiry := getLeaseExpiresAtForTest(t, db, sagaID)
	var secondExpiry time.Time
	awaitErr2 := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			secondExpiry = getLeaseExpiresAtForTest(t, db, sagaID)
			return secondExpiry.After(firstExpiry)
		})
	require.NoError(t, awaitErr2, "Lease should continue to be renewed")

	// The claimed_at should have been updated
	assert.True(t, secondExpiry.After(firstExpiry),
		"Lease should continue to be renewed")
}

func TestLeaseRenewer_StopsOnContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-lease-2"
	sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(50*time.Millisecond))

	// Track goroutine count before starting
	initialGoroutines := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	renewer.Start(ctx)

	// Verify goroutine is running
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() > initialGoroutines
		})
	require.NoError(t, err, "Renewer goroutine should be running")

	// Cancel context
	cancel()

	// Wait for goroutine to exit
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() <= initialGoroutines
		})
	require.NoError(t, err, "Renewer goroutine should stop after context cancellation")
}

func TestLeaseRenewer_StopsOnStopCall(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-lease-3"
	sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(50*time.Millisecond))

	ctx := context.Background()
	// Capture baseline before Start so the goroutine is not yet counted
	initialGoroutinesStop := runtime.NumGoroutine()
	renewer.Start(ctx)

	// Verify renewer is running by waiting for the background goroutine to appear
	awaitStopErr := await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() > initialGoroutinesStop
		})
	require.NoError(t, awaitStopErr, "Renewer goroutine should be running before Stop")

	// Call Stop and verify it completes within reasonable time
	stopped := make(chan struct{})
	go func() {
		renewer.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Stop() did not complete within 1 second")
	}
}

func TestLeaseRenewer_StopIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-lease-4"
	sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(50*time.Millisecond))

	ctx := context.Background()
	// Capture baseline before Start so the goroutine is not yet counted
	initialGoroutinesIdempotent := runtime.NumGoroutine()
	renewer.Start(ctx)

	awaitIdempotentErr := await.New().
		AtMost(1 * time.Second).
		PollInterval(10 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() > initialGoroutinesIdempotent
		})
	require.NoError(t, awaitIdempotentErr, "Renewer goroutine should be running before Stop")

	// Call Stop() multiple times - should not panic
	renewer.Stop()
	renewer.Stop()
	renewer.Stop()
}

func TestLeaseRenewer_ContinuesOnRenewalFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-lease-5"
	sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var renewalCount atomic.Int32

	// Create renewer with callback to count renewals
	renewer := NewLeaseRenewer(sagaID, claimService, logger,
		WithRenewalInterval(50*time.Millisecond),
		WithRenewalCallback(func() {
			renewalCount.Add(1)
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	renewer.Start(ctx)
	defer renewer.Stop()

	// Wait for multiple renewals
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return renewalCount.Load() >= 3
		})
	require.NoError(t, err, "Should have multiple renewals")

	// Now delete the saga to cause renewal failures
	db.Exec("DELETE FROM saga_instances WHERE id = ?", sagaID)

	// Renewer should continue running (not crash) even with failures
	beforeCount := renewalCount.Load()
	awaitContinueErr := await.New().
		AtMost(2 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return renewalCount.Load() > beforeCount
		})
	require.NoError(t, awaitContinueErr, "Renewer should continue attempting renewals despite failures")

	// The callback should still be called even though the DB operation fails
	assert.Greater(t, renewalCount.Load(), beforeCount, "Renewer should continue attempting renewals despite failures")
}

func TestLeaseRenewer_ConfigurationFromEnvironment(t *testing.T) {
	// Test that NewLeaseRenewalConfig reads from environment
	t.Setenv("SAGA_LEASE_RENEWAL_INTERVAL", "3m")

	config := NewLeaseRenewalConfig()
	assert.Equal(t, 3*time.Minute, config.RenewalInterval)
}

func TestLeaseRenewer_DefaultConfiguration(t *testing.T) {
	// Without env var, should use default of 2 minutes
	os.Unsetenv("SAGA_LEASE_RENEWAL_INTERVAL")
	config := NewLeaseRenewalConfig()
	assert.Equal(t, 2*time.Minute, config.RenewalInterval)
}

func TestLeaseRenewer_NoGoroutineLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	initialGoroutines := runtime.NumGoroutine()

	// Start and stop multiple renewers
	for i := 0; i < 5; i++ {
		podID := "test-pod-leak-" + string(rune('a'+i))
		sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

		claimService := NewClaimService(db, &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			PodID:         podID,
		})

		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
		renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(50*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		preLeak := runtime.NumGoroutine()
		renewer.Start(ctx)
		awaitLeakErr := await.New().
			AtMost(1 * time.Second).
			PollInterval(10 * time.Millisecond).
			Until(func() bool {
				return runtime.NumGoroutine() > preLeak
			})
		require.NoError(t, awaitLeakErr, "Renewer goroutine should start")
		cancel()
		renewer.Stop()
	}

	// Wait for goroutines to clean up
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+1 // Allow 1 for test framework
		})
	require.NoError(t, err, "Should not leak goroutines")
}

func TestLeaseRenewer_StartIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestPostgres(t)
	t.Cleanup(cleanup)

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	podID := "test-pod-start-idempotent"
	sagaID := createLeaseTestSaga(t, db, podID, time.Now().Add(5*time.Minute))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		PodID:         podID,
	})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	renewer := NewLeaseRenewer(sagaID, claimService, logger, WithRenewalInterval(50*time.Millisecond))

	ctx := context.Background()

	initialGoroutines := runtime.NumGoroutine()

	// Start multiple times
	renewer.Start(ctx)
	renewer.Start(ctx) // Should be ignored
	renewer.Start(ctx) // Should be ignored

	// Poll until goroutine count settles. Using await tolerates transient
	// runtime jitter (GC, timers, concurrent tests) while still catching a
	// real leak — 3 unguarded Start() calls would add +3, exceeding +2.
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			return runtime.NumGoroutine() <= initialGoroutines+2
		})
	assert.NoError(t, err, "Multiple Start() calls should not spawn multiple goroutines")

	renewer.Stop()
}
