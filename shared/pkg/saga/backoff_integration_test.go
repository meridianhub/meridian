package saga

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateNextRetryAt_PersistsValue verifies the repository sets next_retry_at
// and that subsequent reads observe the value.
func TestUpdateNextRetryAt_PersistsValue(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewGormSagaInstanceRepository(db)
	ctx := context.Background()

	instance := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, repo.Create(ctx, instance))

	// next_retry_at starts NULL.
	fetched, err := repo.FindByID(ctx, instance.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Nil(t, fetched.NextRetryAt, "next_retry_at should default to NULL")

	target := time.Now().Add(30 * time.Second).UTC().Truncate(time.Microsecond)
	require.NoError(t, repo.UpdateNextRetryAt(ctx, instance.ID, target))

	fetched, err = repo.FindByID(ctx, instance.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	require.NotNil(t, fetched.NextRetryAt, "next_retry_at should be persisted")
	assert.WithinDuration(t, target, *fetched.NextRetryAt, time.Second,
		"persisted next_retry_at should round-trip within DB precision")
}

// TestBackoff_OrphanWatcherSkipsThenReclaims is the end-to-end safety net for
// the backoff feature: after handleTransientFailure sets next_retry_at into the
// future, the orphan watcher must skip the saga; after the backoff window
// elapses the saga must become eligible again.
//
// We model "time elapsing" by directly rewriting next_retry_at to the past,
// rather than sleeping for the real duration. This keeps the test fast.
func TestBackoff_OrphanWatcherSkipsThenReclaims(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Insert an orphaned saga with no backoff in effect.
	expiredLease := time.Now().Add(-10 * time.Minute)
	repo := NewGormSagaInstanceRepository(db)
	instance := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		LeaseExpiresAt:   &expiredLease,
	}
	require.NoError(t, repo.Create(ctx, instance))

	// 2. Simulate handleTransientFailure: set next_retry_at to 1 hour in the future.
	future := time.Now().Add(1 * time.Hour)
	require.NoError(t, repo.UpdateNextRetryAt(ctx, instance.ID, future))

	claimService := NewClaimService(db, &ClaimConfig{
		LeaseDuration:  5 * time.Minute,
		BatchSize:      10,
		MaxJitterMS:    0,
		MaxReplays:     DefaultMaxReplays,
		RetryBaseDelay: 1 * time.Second,
		RetryMaxDelay:  5 * time.Minute,
		PodID:          "test-pod",
	})

	// 3. Watcher should NOT reclaim - saga is mid-backoff.
	claimed, err := claimService.ClaimOrphanedSagas(ctx)
	require.NoError(t, err)
	assert.NotContains(t, claimed, instance.ID,
		"saga with future next_retry_at must be skipped by orphan watcher")

	// 4. Simulate backoff window elapsing: rewrite next_retry_at to the past.
	past := time.Now().Add(-1 * time.Minute)
	require.NoError(t, repo.UpdateNextRetryAt(ctx, instance.ID, past))

	// 5. Watcher reclaims now that the window has elapsed.
	claimed, err = claimService.ClaimOrphanedSagas(ctx)
	require.NoError(t, err)
	assert.Contains(t, claimed, instance.ID,
		"saga should be reclaimable after next_retry_at elapses")
}

// TestResetReplayCountAndBackoff_ClearsBoth verifies the atomic reset method
// clears replay_count AND next_retry_at in a single UPDATE.
func TestResetReplayCountAndBackoff_ClearsBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewGormSagaInstanceRepository(db)
	ctx := context.Background()

	retry := time.Now().Add(1 * time.Minute)
	instance := &SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
		ReplayCount:      3,
		NextRetryAt:      &retry,
	}
	require.NoError(t, repo.Create(ctx, instance))

	require.NoError(t, repo.ResetReplayCountAndBackoff(ctx, instance.ID))

	fetched, err := repo.FindByID(ctx, instance.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, 0, fetched.ReplayCount, "replay_count must be reset to 0")
	assert.Nil(t, fetched.NextRetryAt, "next_retry_at must be cleared to NULL")
}
