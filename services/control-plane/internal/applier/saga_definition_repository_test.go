package applier

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSagaDefinitionRepository_PgxIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	repo := NewSagaDefinitionRepository(pool)
	ctx := context.Background()

	t.Run("FindOrCreate_NewRow", func(t *testing.T) {
		script := "def main(): return {'ok': True}"
		def, err := repo.FindOrCreate(ctx, "test_saga_new", "1.0.0", script, nil)
		require.NoError(t, err)
		require.NotNil(t, def)

		assert.NotEqual(t, uuid.Nil, def.ID)
		assert.Equal(t, "test_saga_new", def.Name)
		assert.Equal(t, "1.0.0", def.Version)
		assert.Equal(t, script, def.Script)
		assert.Equal(t, saga.ComputeSagaDefinitionScriptHash(script), def.ScriptHash)
		assert.False(t, def.CreatedAt.IsZero())
	})

	t.Run("FindOrCreate_Idempotent", func(t *testing.T) {
		script := "def main(): return None"

		first, err := repo.FindOrCreate(ctx, "saga_dup_pgx", "1.0.0", script, nil)
		require.NoError(t, err)

		second, err := repo.FindOrCreate(ctx, "saga_dup_pgx", "1.0.0", script, nil)
		require.NoError(t, err)

		assert.Equal(t, first.ID, second.ID, "same (name, version, script) must return same row")
	})

	t.Run("FindOrCreate_HashMismatchRejected", func(t *testing.T) {
		_, err := repo.FindOrCreate(ctx, "immutable_pgx", "2.0.0", "def v1(): pass", nil)
		require.NoError(t, err)

		_, err = repo.FindOrCreate(ctx, "immutable_pgx", "2.0.0", "def v1_TAMPERED(): pass", nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, saga.ErrSagaDefinitionHashMismatch),
			"expected ErrSagaDefinitionHashMismatch, got %v", err)
	})

	t.Run("FindOrCreate_PersistsParamsSchema", func(t *testing.T) {
		params := saga.JSONB{"amount": map[string]any{"type": "Decimal", "required": true}}
		def, err := repo.FindOrCreate(ctx, "schema_pgx", "1.0.0", "def main(): pass", params)
		require.NoError(t, err)
		require.NotNil(t, def.ParamsSchema)

		loaded, err := repo.FindByID(ctx, def.ID)
		require.NoError(t, err)
		require.NotNil(t, loaded.ParamsSchema)
		assert.Contains(t, loaded.ParamsSchema, "amount")
	})

	t.Run("FindByID_Success", func(t *testing.T) {
		created, err := repo.FindOrCreate(ctx, "lookup_pgx", "1.0.0", "def main(): pass", nil)
		require.NoError(t, err)

		loaded, err := repo.FindByID(ctx, created.ID)
		require.NoError(t, err)
		require.NotNil(t, loaded)

		assert.Equal(t, created.ID, loaded.ID)
		assert.Equal(t, created.Script, loaded.Script)
		assert.Equal(t, created.ScriptHash, loaded.ScriptHash)
	})

	t.Run("FindByID_NotFound", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.New())
		require.Error(t, err)
		assert.True(t, errors.Is(err, saga.ErrSagaDefinitionNotFound))
	})

	t.Run("FindByID_NilUUIDReturnsNotFound", func(t *testing.T) {
		_, err := repo.FindByID(ctx, uuid.Nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, saga.ErrSagaDefinitionNotFound))
	})

	t.Run("Constructor_NilPoolReturnsNil", func(t *testing.T) {
		assert.Nil(t, NewSagaDefinitionRepository(nil))
	})
}
