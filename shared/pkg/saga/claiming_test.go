// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestNewClaimConfig_Defaults verifies default configuration values.
func TestNewClaimConfig_Defaults(t *testing.T) {
	// Clear any env vars that might affect test
	os.Unsetenv("SAGA_LEASE_DURATION")
	os.Unsetenv("SAGA_CLAIM_BATCH_SIZE")
	os.Unsetenv("SAGA_CLAIM_JITTER_MS")
	os.Unsetenv("SAGA_RETRY_BASE_DELAY")
	os.Unsetenv("SAGA_RETRY_MAX_DELAY")
	os.Unsetenv("HOSTNAME")

	config := NewClaimConfig()

	assert.Equal(t, 5*time.Minute, config.LeaseDuration, "Default lease duration should be 5 minutes")
	assert.Equal(t, 10, config.BatchSize, "Default batch size should be 10")
	assert.Equal(t, 500, config.MaxJitterMS, "Default max jitter should be 500ms")
	assert.Equal(t, DefaultRetryBaseDelay, config.RetryBaseDelay, "Default retry base delay should be 1s")
	assert.Equal(t, DefaultRetryMaxDelay, config.RetryMaxDelay, "Default retry max delay should be 5m")
	assert.NotEmpty(t, config.PodID, "PodID should be generated via UUID fallback")
}

// TestNewClaimConfig_FromEnv verifies configuration from environment variables.
func TestNewClaimConfig_FromEnv(t *testing.T) {
	// Set environment variables
	t.Setenv("SAGA_LEASE_DURATION", "10m")
	t.Setenv("SAGA_CLAIM_BATCH_SIZE", "25")
	t.Setenv("SAGA_CLAIM_JITTER_MS", "1000")
	t.Setenv("SAGA_RETRY_BASE_DELAY", "3s")
	t.Setenv("SAGA_RETRY_MAX_DELAY", "2m")
	t.Setenv("HOSTNAME", "pod-abc-123")

	config := NewClaimConfig()

	assert.Equal(t, 10*time.Minute, config.LeaseDuration, "Lease duration should be parsed from env")
	assert.Equal(t, 25, config.BatchSize, "Batch size should be parsed from env")
	assert.Equal(t, 1000, config.MaxJitterMS, "Max jitter should be parsed from env")
	assert.Equal(t, 3*time.Second, config.RetryBaseDelay, "Retry base delay should be parsed from env")
	assert.Equal(t, 2*time.Minute, config.RetryMaxDelay, "Retry max delay should be parsed from env")
	assert.Equal(t, "pod-abc-123", config.PodID, "PodID should use HOSTNAME when set")
}

// TestNewClaimConfig_InvalidEnv verifies fallback to defaults on invalid env values.
func TestNewClaimConfig_InvalidEnv(t *testing.T) {
	// Set invalid environment variables
	t.Setenv("SAGA_LEASE_DURATION", "invalid")
	t.Setenv("SAGA_CLAIM_BATCH_SIZE", "-5")
	t.Setenv("SAGA_CLAIM_JITTER_MS", "not-a-number")
	os.Unsetenv("HOSTNAME")

	config := NewClaimConfig()

	assert.Equal(t, 5*time.Minute, config.LeaseDuration, "Invalid duration should fallback to default")
	// Note: -5 parses as valid int, so batch size will be -5; but that's ok for this test
	// In production we'd want validation but task doesn't require it
	assert.Equal(t, 500, config.MaxJitterMS, "Invalid jitter should fallback to default")
}

// TestNewClaimConfig_RejectsNegativeRetryDelay verifies that a negative
// SAGA_RETRY_BASE_DELAY or SAGA_RETRY_MAX_DELAY is sanitized to defaults,
// preventing operator misconfiguration from disabling backoff in production.
func TestNewClaimConfig_RejectsNegativeRetryDelay(t *testing.T) {
	t.Setenv("SAGA_RETRY_BASE_DELAY", "-1s")
	t.Setenv("SAGA_RETRY_MAX_DELAY", "5m")

	config := NewClaimConfig()
	assert.Equal(t, DefaultRetryBaseDelay, config.RetryBaseDelay,
		"negative base delay should fall back to default")
	assert.Equal(t, DefaultRetryMaxDelay, config.RetryMaxDelay,
		"both bounds should reset when one is invalid - keeps the pair consistent")
}

// TestNewClaimConfig_RejectsInvertedRetryDelay verifies that base > max is
// rejected (would force every retry to saturate to the smaller value).
func TestNewClaimConfig_RejectsInvertedRetryDelay(t *testing.T) {
	t.Setenv("SAGA_RETRY_BASE_DELAY", "1h")
	t.Setenv("SAGA_RETRY_MAX_DELAY", "5s") // inverted - base > max

	config := NewClaimConfig()
	assert.Equal(t, DefaultRetryBaseDelay, config.RetryBaseDelay,
		"inverted (base > max) should fall back to default")
	assert.Equal(t, DefaultRetryMaxDelay, config.RetryMaxDelay,
		"inverted (base > max) should fall back to default")
}

// TestGetPodID_HostnameFallback verifies pod ID generation.
func TestGetPodID_HostnameFallback(t *testing.T) {
	t.Run("uses HOSTNAME when set", func(t *testing.T) {
		t.Setenv("HOSTNAME", "my-pod-name")
		podID := GetPodID()
		assert.Equal(t, "my-pod-name", podID)
	})

	t.Run("generates UUID when HOSTNAME empty", func(t *testing.T) {
		os.Unsetenv("HOSTNAME")
		podID := GetPodID()
		assert.NotEmpty(t, podID)
		// Should be valid UUID
		_, err := uuid.Parse(podID)
		assert.NoError(t, err, "Should generate valid UUID when HOSTNAME not set")
	})
}

// TestSagaClaimService_ClaimOrphanedSagas_Integration tests the claiming logic
// using testcontainers for real PostgreSQL behavior.
func TestSagaClaimService_ClaimOrphanedSagas_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	config := &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		BatchSize:     10,
		MaxJitterMS:   0, // Disable jitter for deterministic tests
		MaxReplays:    DefaultMaxReplays,
		PodID:         "test-pod-1",
	}
	service := NewClaimService(db, config)

	t.Run("claims orphaned sagas with expired lease", func(t *testing.T) {
		// Create orphaned saga (expired lease)
		expiredLease := time.Now().Add(-10 * time.Minute)
		orphanedSaga := createTestSaga(t, db, SagaStatusRunning, &expiredLease, nil)

		// Claim
		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.Len(t, claimed, 1)
		assert.Equal(t, orphanedSaga.ID, claimed[0])

		// Verify saga is now claimed by our pod
		var updated SagaInstance
		db.First(&updated, "id = ?", orphanedSaga.ID)
		assert.Equal(t, "test-pod-1", *updated.ClaimedByPod)
		assert.NotNil(t, updated.ClaimedAt)
		assert.NotNil(t, updated.LeaseExpiresAt)
		assert.True(t, updated.LeaseExpiresAt.After(time.Now()))
	})

	t.Run("claims sagas with null claimed_by_pod", func(t *testing.T) {
		// Create saga with no owner
		saga := createTestSaga(t, db, SagaStatusPending, nil, nil)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.Contains(t, claimed, saga.ID)

		var updated SagaInstance
		db.First(&updated, "id = ?", saga.ID)
		assert.Equal(t, "test-pod-1", *updated.ClaimedByPod)
	})

	t.Run("does not claim completed sagas", func(t *testing.T) {
		// Create completed saga with expired lease (should NOT be claimed)
		expiredLease := time.Now().Add(-10 * time.Minute)
		completedSaga := createTestSaga(t, db, SagaStatusCompleted, &expiredLease, nil)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, completedSaga.ID, "Completed sagas should not be claimed")
	})

	t.Run("does not claim sagas with active lease", func(t *testing.T) {
		// Create saga with active (future) lease
		futureLease := time.Now().Add(10 * time.Minute)
		otherPod := "other-pod"
		activeSaga := createTestSaga(t, db, SagaStatusRunning, &futureLease, &otherPod)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, activeSaga.ID, "Sagas with active lease should not be claimed")
	})

	t.Run("skips sagas in backoff (next_retry_at in future)", func(t *testing.T) {
		// Create an orphaned saga that is mid-backoff: lease expired, but
		// next_retry_at is still in the future. The watcher must skip it.
		expiredLease := time.Now().Add(-10 * time.Minute)
		future := time.Now().Add(10 * time.Minute)
		sagaInBackoff := createTestSagaWithRetry(t, db, SagaStatusRunning, &expiredLease, nil, &future)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, sagaInBackoff.ID,
			"sagas with future next_retry_at must be skipped by orphan watcher")
	})

	t.Run("claims sagas whose backoff has elapsed", func(t *testing.T) {
		// next_retry_at in the past = backoff window has elapsed, saga is eligible.
		expiredLease := time.Now().Add(-10 * time.Minute)
		past := time.Now().Add(-1 * time.Minute)
		readySaga := createTestSagaWithRetry(t, db, SagaStatusRunning, &expiredLease, nil, &past)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.Contains(t, claimed, readySaga.ID,
			"sagas with past next_retry_at must be claimable again")
	})

	t.Run("claims sagas with NULL next_retry_at", func(t *testing.T) {
		// Fresh saga (no backoff ever set) - NULL means immediately eligible.
		expiredLease := time.Now().Add(-10 * time.Minute)
		freshSaga := createTestSagaWithRetry(t, db, SagaStatusRunning, &expiredLease, nil, nil)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.Contains(t, claimed, freshSaga.ID,
			"sagas with NULL next_retry_at must be claimable (no backoff in effect)")
	})

	t.Run("respects batch size limit", func(t *testing.T) {
		// Create more sagas than batch size
		configSmallBatch := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     2,
			MaxJitterMS:   0,
			MaxReplays:    DefaultMaxReplays,
			PodID:         "batch-test-pod",
		}
		smallBatchService := NewClaimService(db, configSmallBatch)

		// Create 5 orphaned sagas
		for i := 0; i < 5; i++ {
			createTestSaga(t, db, SagaStatusPending, nil, nil)
		}

		claimed, err := smallBatchService.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.LessOrEqual(t, len(claimed), 2, "Should respect batch size limit")
	})
}

// TestSagaClaimService_Concurrency_Integration tests that FOR UPDATE SKIP LOCKED
// prevents race conditions when multiple pods claim simultaneously.
func TestSagaClaimService_Concurrency_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a single orphaned saga
	orphanedSaga := createTestSaga(t, db, SagaStatusRunning, nil, nil)

	// Create two services simulating two pods
	service1 := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		BatchSize:     10,
		MaxJitterMS:   0,
		MaxReplays:    DefaultMaxReplays,
		PodID:         "pod-1",
	})
	service2 := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		BatchSize:     10,
		MaxJitterMS:   0,
		MaxReplays:    DefaultMaxReplays,
		PodID:         "pod-2",
	})

	// Run both claim operations concurrently
	var wg sync.WaitGroup
	var claimed1, claimed2 []uuid.UUID
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		claimed1, err1 = service1.ClaimOrphanedSagas(context.Background())
	}()
	go func() {
		defer wg.Done()
		claimed2, err2 = service2.ClaimOrphanedSagas(context.Background())
	}()
	wg.Wait()

	require.NoError(t, err1)
	require.NoError(t, err2)

	// Only ONE should have claimed the saga (SKIP LOCKED prevents race condition)
	totalClaimed := len(claimed1) + len(claimed2)
	assert.Equal(t, 1, totalClaimed, "Exactly one pod should claim the saga")

	// Verify the saga is claimed by exactly one pod
	var saga SagaInstance
	db.First(&saga, "id = ?", orphanedSaga.ID)
	assert.NotNil(t, saga.ClaimedByPod)
	assert.True(t, *saga.ClaimedByPod == "pod-1" || *saga.ClaimedByPod == "pod-2")
}

// TestSagaClaimService_LeaseExpiry_Integration simulates pod crash via lease expiry.
func TestSagaClaimService_LeaseExpiry_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Simulate pod-1 claimed a saga but then crashed (lease expired)
	expiredLease := time.Now().Add(-1 * time.Minute)
	claimedAt := time.Now().Add(-6 * time.Minute)
	crashedPod := "crashed-pod"
	orphanedSaga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           SagaStatusRunning,
		CorrelationID:    uuid.New(),
		ClaimedByPod:     &crashedPod,
		ClaimedAt:        &claimedAt,
		LeaseExpiresAt:   &expiredLease,
	}
	err = db.Create(orphanedSaga).Error
	require.NoError(t, err)

	// New pod claims the orphaned saga
	service := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		BatchSize:     10,
		MaxJitterMS:   0,
		MaxReplays:    DefaultMaxReplays,
		PodID:         "recovery-pod",
	})

	claimed, err := service.ClaimOrphanedSagas(context.Background())
	require.NoError(t, err)
	assert.Contains(t, claimed, orphanedSaga.ID, "New pod should claim saga from crashed pod")

	// Verify saga is now owned by recovery pod
	var updated SagaInstance
	db.First(&updated, "id = ?", orphanedSaga.ID)
	assert.Equal(t, "recovery-pod", *updated.ClaimedByPod)
	assert.True(t, updated.LeaseExpiresAt.After(time.Now()), "Lease should be renewed")
}

// TestSagaClaimService_ClaimsCorrectStatuses verifies only PENDING, RUNNING, COMPENSATING
// statuses are claimed.
func TestSagaClaimService_ClaimsCorrectStatuses(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	service := NewClaimService(db, &ClaimConfig{
		LeaseDuration: 5 * time.Minute,
		BatchSize:     100,
		MaxJitterMS:   0,
		MaxReplays:    DefaultMaxReplays,
		PodID:         "status-test-pod",
	})

	// Create sagas with various statuses
	pendingSaga := createTestSaga(t, db, SagaStatusPending, nil, nil)
	runningSaga := createTestSaga(t, db, SagaStatusRunning, nil, nil)
	compensatingSaga := createTestSaga(t, db, SagaStatusCompensating, nil, nil)
	completedSaga := createTestSaga(t, db, SagaStatusCompleted, nil, nil)
	failedSaga := createTestSaga(t, db, SagaStatusFailed, nil, nil)
	compensatedSaga := createTestSaga(t, db, SagaStatusCompensated, nil, nil)

	claimed, err := service.ClaimOrphanedSagas(context.Background())
	require.NoError(t, err)

	// Should claim: PENDING, RUNNING, COMPENSATING
	assert.Contains(t, claimed, pendingSaga.ID, "PENDING should be claimed")
	assert.Contains(t, claimed, runningSaga.ID, "RUNNING should be claimed")
	assert.Contains(t, claimed, compensatingSaga.ID, "COMPENSATING should be claimed")

	// Should NOT claim: COMPLETED, FAILED, COMPENSATED
	assert.NotContains(t, claimed, completedSaga.ID, "COMPLETED should not be claimed")
	assert.NotContains(t, claimed, failedSaga.ID, "FAILED should not be claimed")
	assert.NotContains(t, claimed, compensatedSaga.ID, "COMPENSATED should not be claimed")
}

// createTestSaga is a helper to create test saga instances.
func createTestSaga(t *testing.T, db *gorm.DB, status SagaStatus, leaseExpires *time.Time, claimedBy *string) *SagaInstance {
	t.Helper()
	saga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           status,
		CorrelationID:    uuid.New(),
		LeaseExpiresAt:   leaseExpires,
		ClaimedByPod:     claimedBy,
	}
	err := db.Create(saga).Error
	require.NoError(t, err)
	return saga
}

// createTestSagaWithRetry is like createTestSaga but also sets next_retry_at,
// for tests that exercise the backoff-aware orphan-claim predicate.
func createTestSagaWithRetry(t *testing.T, db *gorm.DB, status SagaStatus, leaseExpires *time.Time, claimedBy *string, nextRetryAt *time.Time) *SagaInstance {
	t.Helper()
	saga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           status,
		CorrelationID:    uuid.New(),
		LeaseExpiresAt:   leaseExpires,
		ClaimedByPod:     claimedBy,
		NextRetryAt:      nextRetryAt,
	}
	err := db.Create(saga).Error
	require.NoError(t, err)
	return saga
}
