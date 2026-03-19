package applier

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyJobRepository_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	repo := NewApplyJobRepository(pool)
	ctx := context.Background()

	t.Run("Create", func(t *testing.T) {
		job, err := repo.Create(ctx, 1)
		require.NoError(t, err)
		require.NotNil(t, job)

		assert.NotEqual(t, uuid.Nil, job.ID)
		assert.Equal(t, 1, job.ManifestVersion)
		assert.Equal(t, ApplyJobStatusPending, job.Status)
		assert.Empty(t, job.Error)
		assert.Nil(t, job.SagaExecutionID)
		assert.Nil(t, job.CompletedAt)
		assert.False(t, job.CreatedAt.IsZero())
	})

	t.Run("GetByID", func(t *testing.T) {
		created, err := repo.Create(ctx, 5)
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, created.ID)
		require.NoError(t, err)
		require.NotNil(t, found)

		assert.Equal(t, created.ID, found.ID)
		assert.Equal(t, 5, found.ManifestVersion)
		assert.Equal(t, ApplyJobStatusPending, found.Status)
		assert.Nil(t, found.SagaExecutionID)
		assert.Empty(t, found.Error)
	})

	t.Run("GetByID_NotFound", func(t *testing.T) {
		_, err := repo.GetByID(ctx, uuid.New())
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("GetByManifestVersion", func(t *testing.T) {
		_, err := repo.Create(ctx, 100)
		require.NoError(t, err)

		second, err := repo.Create(ctx, 100)
		require.NoError(t, err)

		found, err := repo.GetByManifestVersion(ctx, 100)
		require.NoError(t, err)
		require.NotNil(t, found)
		assert.Equal(t, second.ID, found.ID)
	})

	t.Run("GetByManifestVersion_NotFound", func(t *testing.T) {
		_, err := repo.GetByManifestVersion(ctx, 9999)
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkApplying", func(t *testing.T) {
		job, err := repo.Create(ctx, 200)
		require.NoError(t, err)

		sagaID := uuid.New()
		err = repo.MarkApplying(ctx, job.ID, sagaID)
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusApplying, found.Status)
		require.NotNil(t, found.SagaExecutionID)
		assert.Equal(t, sagaID, *found.SagaExecutionID)
	})

	t.Run("MarkApplying_NotFound", func(t *testing.T) {
		err := repo.MarkApplying(ctx, uuid.New(), uuid.New())
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkApplying_WrongStatus", func(t *testing.T) {
		job, err := repo.Create(ctx, 201)
		require.NoError(t, err)

		err = repo.MarkApplying(ctx, job.ID, uuid.New())
		require.NoError(t, err)

		// Already APPLYING, cannot mark APPLYING again
		err = repo.MarkApplying(ctx, job.ID, uuid.New())
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkApplied", func(t *testing.T) {
		job, err := repo.Create(ctx, 300)
		require.NoError(t, err)

		err = repo.MarkApplying(ctx, job.ID, uuid.New())
		require.NoError(t, err)

		err = repo.MarkApplied(ctx, job.ID)
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusApplied, found.Status)
		assert.NotNil(t, found.CompletedAt)
	})

	t.Run("MarkApplied_NotFound", func(t *testing.T) {
		err := repo.MarkApplied(ctx, uuid.New())
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkApplied_WrongStatus", func(t *testing.T) {
		job, err := repo.Create(ctx, 301)
		require.NoError(t, err)

		// PENDING -> cannot MarkApplied (needs APPLYING)
		err = repo.MarkApplied(ctx, job.ID)
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkFailed_FromPending", func(t *testing.T) {
		job, err := repo.Create(ctx, 400)
		require.NoError(t, err)

		err = repo.MarkFailed(ctx, job.ID, "something went wrong")
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusFailed, found.Status)
		assert.Equal(t, "something went wrong", found.Error)
		assert.NotNil(t, found.CompletedAt)
	})

	t.Run("MarkFailed_FromApplying", func(t *testing.T) {
		job, err := repo.Create(ctx, 401)
		require.NoError(t, err)

		err = repo.MarkApplying(ctx, job.ID, uuid.New())
		require.NoError(t, err)

		err = repo.MarkFailed(ctx, job.ID, "saga execution error")
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusFailed, found.Status)
		assert.Equal(t, "saga execution error", found.Error)
	})

	t.Run("MarkFailed_NotFound", func(t *testing.T) {
		err := repo.MarkFailed(ctx, uuid.New(), "error")
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("MarkFailed_WrongStatus_Applied", func(t *testing.T) {
		job, err := repo.Create(ctx, 402)
		require.NoError(t, err)

		err = repo.MarkApplying(ctx, job.ID, uuid.New())
		require.NoError(t, err)
		err = repo.MarkApplied(ctx, job.ID)
		require.NoError(t, err)

		// Cannot fail an APPLIED job
		err = repo.MarkFailed(ctx, job.ID, "too late")
		require.ErrorIs(t, err, ErrJobNotFound)
	})

	t.Run("FullLifecycle", func(t *testing.T) {
		// Create
		job, err := repo.Create(ctx, 500)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusPending, job.Status)

		// Mark applying
		sagaID := uuid.New()
		err = repo.MarkApplying(ctx, job.ID, sagaID)
		require.NoError(t, err)

		found, err := repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusApplying, found.Status)
		require.NotNil(t, found.SagaExecutionID)
		assert.Equal(t, sagaID, *found.SagaExecutionID)

		// Mark applied
		err = repo.MarkApplied(ctx, job.ID)
		require.NoError(t, err)

		found, err = repo.GetByID(ctx, job.ID)
		require.NoError(t, err)
		assert.Equal(t, ApplyJobStatusApplied, found.Status)
		assert.NotNil(t, found.CompletedAt)

		// Retrievable by manifest version
		byVersion, err := repo.GetByManifestVersion(ctx, 500)
		require.NoError(t, err)
		assert.Equal(t, job.ID, byVersion.ID)
		assert.Equal(t, ApplyJobStatusApplied, byVersion.Status)
	})
}
