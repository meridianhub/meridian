// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestNewClaimConfig_MaxReplays verifies MaxReplays configuration.
func TestNewClaimConfig_MaxReplays(t *testing.T) {
	t.Run("uses default when not set", func(t *testing.T) {
		os.Unsetenv("SAGA_MAX_REPLAYS")

		config := NewClaimConfig()

		assert.Equal(t, DefaultMaxReplays, config.MaxReplays, "Should use DefaultMaxReplays")
		assert.Equal(t, 5, config.MaxReplays, "DefaultMaxReplays should be 5")
	})

	t.Run("uses SAGA_MAX_REPLAYS from env", func(t *testing.T) {
		t.Setenv("SAGA_MAX_REPLAYS", "10")

		config := NewClaimConfig()

		assert.Equal(t, 10, config.MaxReplays, "Should use SAGA_MAX_REPLAYS from env")
	})

	t.Run("falls back to default on invalid env", func(t *testing.T) {
		t.Setenv("SAGA_MAX_REPLAYS", "invalid")

		config := NewClaimConfig()

		assert.Equal(t, DefaultMaxReplays, config.MaxReplays, "Invalid value should fallback to default")
	})
}

// TestSagaClaimService_ZombieDetection_Integration tests zombie saga detection and transition.
func TestSagaClaimService_ZombieDetection_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	t.Run("transitions zombie saga to FAILED_MANUAL_INTERVENTION", func(t *testing.T) {
		// Create a saga that has exceeded max replays
		zombieSaga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 5)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5, // Saga has replay_count=5, which equals MaxReplays
			PodID:         "test-pod",
		}
		service := NewClaimService(db, config)

		// Claim should NOT include the zombie saga
		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, zombieSaga.ID, "Zombie saga should not be claimed")

		// Verify saga was transitioned to FAILED_MANUAL_INTERVENTION
		var updated SagaInstance
		db.First(&updated, "id = ?", zombieSaga.ID)
		assert.Equal(t, SagaStatusFailedManualIntervention, updated.Status, "Zombie should be transitioned")
		assert.Nil(t, updated.ClaimedByPod, "Zombie should not be claimed")
		assert.Nil(t, updated.LeaseExpiresAt, "Zombie should have no lease")
	})

	t.Run("claims healthy saga while transitioning zombie", func(t *testing.T) {
		// Create one zombie (replay_count >= MaxReplays)
		zombieSaga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 10)
		// Create one healthy saga (replay_count < MaxReplays)
		healthySaga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 2)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "test-pod-2",
		}
		service := NewClaimService(db, config)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)

		// Should claim healthy saga
		assert.Contains(t, claimed, healthySaga.ID, "Healthy saga should be claimed")
		// Should NOT claim zombie
		assert.NotContains(t, claimed, zombieSaga.ID, "Zombie saga should not be claimed")

		// Verify healthy saga's replay_count was incremented
		var healthy SagaInstance
		db.First(&healthy, "id = ?", healthySaga.ID)
		assert.Equal(t, 3, healthy.ReplayCount, "Healthy saga replay_count should be incremented")
		assert.Equal(t, "test-pod-2", *healthy.ClaimedByPod, "Healthy saga should be claimed by pod")

		// Verify zombie was transitioned
		var zombie SagaInstance
		db.First(&zombie, "id = ?", zombieSaga.ID)
		assert.Equal(t, SagaStatusFailedManualIntervention, zombie.Status, "Zombie should be transitioned")
	})

	t.Run("saga becomes zombie on replay attempt after exceeding threshold", func(t *testing.T) {
		// Create saga at threshold - 1 (will be claimed, then on next claim will be zombie)
		saga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 4)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "test-pod-3",
		}
		service := NewClaimService(db, config)

		// First claim - should succeed and increment replay_count to 5
		claimed1, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.Contains(t, claimed1, saga.ID, "Should claim saga at threshold-1")

		// Verify replay_count is now 5 (at threshold)
		var afterFirst SagaInstance
		db.First(&afterFirst, "id = ?", saga.ID)
		assert.Equal(t, 5, afterFirst.ReplayCount, "Replay count should be incremented to 5")

		// Simulate saga failure by expiring lease
		expiredLease := time.Now().Add(-1 * time.Minute)
		db.Model(&SagaInstance{}).Where("id = ?", saga.ID).Updates(map[string]interface{}{
			"lease_expires_at": expiredLease,
		})

		// Second claim attempt - saga should be detected as zombie
		claimed2, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed2, saga.ID, "Should NOT claim zombie saga")

		// Verify saga was transitioned to FAILED_MANUAL_INTERVENTION
		var afterSecond SagaInstance
		db.First(&afterSecond, "id = ?", saga.ID)
		assert.Equal(t, SagaStatusFailedManualIntervention, afterSecond.Status, "Should transition to FAILED_MANUAL_INTERVENTION")
	})

	t.Run("does not transition sagas with active lease as zombies", func(t *testing.T) {
		// Create high replay_count saga with active lease (should NOT be transitioned)
		activeLease := time.Now().Add(10 * time.Minute)
		otherPod := "active-pod"
		saga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, &activeLease, &otherPod, 10)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "test-pod-4",
		}
		service := NewClaimService(db, config)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, saga.ID, "Active lease saga should not be claimed")

		// Verify saga was NOT transitioned (still has original status)
		var updated SagaInstance
		db.First(&updated, "id = ?", saga.ID)
		assert.Equal(t, SagaStatusRunning, updated.Status, "Active saga should keep RUNNING status")
		assert.Equal(t, "active-pod", *updated.ClaimedByPod, "Active saga should keep original owner")
	})

	t.Run("does not transition completed sagas as zombies", func(t *testing.T) {
		// Create completed saga with high replay count (should never be touched)
		saga := createTestSagaWithReplayCount(t, db, SagaStatusCompleted, nil, nil, 20)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "test-pod-5",
		}
		service := NewClaimService(db, config)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		assert.NotContains(t, claimed, saga.ID)

		// Verify saga status unchanged
		var updated SagaInstance
		db.First(&updated, "id = ?", saga.ID)
		assert.Equal(t, SagaStatusCompleted, updated.Status, "Completed saga should stay completed")
	})
}

// TestSagaClaimService_ReplayCountIncrement_Integration tests that replay_count is incremented on claim.
func TestSagaClaimService_ReplayCountIncrement_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	t.Run("increments replay_count when claiming orphaned saga", func(t *testing.T) {
		saga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 0)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "replay-test-pod",
		}
		service := NewClaimService(db, config)

		claimed, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		require.Contains(t, claimed, saga.ID)

		var updated SagaInstance
		db.First(&updated, "id = ?", saga.ID)
		assert.Equal(t, 1, updated.ReplayCount, "replay_count should be incremented from 0 to 1")
	})

	t.Run("increments replay_count for each subsequent claim", func(t *testing.T) {
		saga := createTestSagaWithReplayCount(t, db, SagaStatusRunning, nil, nil, 2)

		config := &ClaimConfig{
			LeaseDuration: 5 * time.Minute,
			BatchSize:     10,
			MaxJitterMS:   0,
			MaxReplays:    5,
			PodID:         "replay-test-pod-2",
		}
		service := NewClaimService(db, config)

		// First claim
		claimed1, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		require.Contains(t, claimed1, saga.ID)

		var after1 SagaInstance
		db.First(&after1, "id = ?", saga.ID)
		assert.Equal(t, 3, after1.ReplayCount, "Should increment from 2 to 3")

		// Expire lease to allow re-claim
		expiredLease := time.Now().Add(-1 * time.Minute)
		db.Model(&SagaInstance{}).Where("id = ?", saga.ID).Updates(map[string]interface{}{
			"lease_expires_at": expiredLease,
		})

		// Second claim
		claimed2, err := service.ClaimOrphanedSagas(context.Background())
		require.NoError(t, err)
		require.Contains(t, claimed2, saga.ID)

		var after2 SagaInstance
		db.First(&after2, "id = ?", saga.ID)
		assert.Equal(t, 4, after2.ReplayCount, "Should increment from 3 to 4")
	})
}

// TestZombieDetection_AllActiveStatuses tests that zombie detection works for all active statuses.
func TestZombieDetection_AllActiveStatuses_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	activeStatuses := []SagaStatus{
		SagaStatusPending,
		SagaStatusRunning,
		SagaStatusCompensating,
	}

	for _, status := range activeStatuses {
		t.Run(string(status), func(t *testing.T) {
			zombieSaga := createTestSagaWithReplayCount(t, db, status, nil, nil, 10)

			config := &ClaimConfig{
				LeaseDuration: 5 * time.Minute,
				BatchSize:     10,
				MaxJitterMS:   0,
				MaxReplays:    5,
				PodID:         "status-test-pod",
			}
			service := NewClaimService(db, config)

			claimed, err := service.ClaimOrphanedSagas(context.Background())
			require.NoError(t, err)
			assert.NotContains(t, claimed, zombieSaga.ID, "Zombie %s should not be claimed", status)

			var updated SagaInstance
			db.First(&updated, "id = ?", zombieSaga.ID)
			assert.Equal(t, SagaStatusFailedManualIntervention, updated.Status,
				"Zombie %s should be transitioned to FAILED_MANUAL_INTERVENTION", status)
		})
	}
}

// createTestSagaWithReplayCount is a helper to create test saga instances with a specific replay count.
func createTestSagaWithReplayCount(t *testing.T, db *gorm.DB, status SagaStatus, leaseExpires *time.Time, claimedBy *string, replayCount int) *SagaInstance {
	t.Helper()
	saga := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		Status:           status,
		CorrelationID:    uuid.New(),
		LeaseExpiresAt:   leaseExpires,
		ClaimedByPod:     claimedBy,
		ReplayCount:      replayCount,
	}
	err := db.Create(saga).Error
	require.NoError(t, err)
	return saga
}
