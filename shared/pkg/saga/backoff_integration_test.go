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
