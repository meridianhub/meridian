package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSagaDefinitionRepository_FindOrCreate_NewRow(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	script := "def main(): return {'ok': True}"
	def, err := repo.FindOrCreate(ctx, "test_saga", 1, script, nil)
	require.NoError(t, err)
	require.NotNil(t, def)

	assert.NotEqual(t, uuid.Nil, def.ID)
	assert.Equal(t, "test_saga", def.Name)
	assert.Equal(t, 1, def.Version)
	assert.Equal(t, script, def.Script)
	assert.Equal(t, ComputeSagaDefinitionScriptHash(script), def.ScriptHash)
	assert.False(t, def.CreatedAt.IsZero())
}

func TestSagaDefinitionRepository_FindOrCreate_Idempotent(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	script := "def main(): return None"

	first, err := repo.FindOrCreate(ctx, "saga_dup", 1, script, nil)
	require.NoError(t, err)

	second, err := repo.FindOrCreate(ctx, "saga_dup", 1, script, nil)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "same (name, version, script) must return same row")
	assert.Equal(t, first.CreatedAt.UnixNano(), second.CreatedAt.UnixNano())

	// Verify only one row exists.
	var count int64
	require.NoError(t, db.Model(&SagaDefinition{}).Where("name = ? AND version = ?", "saga_dup", 1).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestSagaDefinitionRepository_FindOrCreate_NewVersionCreatesNewRow(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	v1, err := repo.FindOrCreate(ctx, "saga_versioned", 1, "def v1(): pass", nil)
	require.NoError(t, err)

	v2, err := repo.FindOrCreate(ctx, "saga_versioned", 2, "def v2(): pass", nil)
	require.NoError(t, err)

	assert.NotEqual(t, v1.ID, v2.ID, "different versions must have different IDs")
	assert.Equal(t, 1, v1.Version)
	assert.Equal(t, 2, v2.Version)
}

func TestSagaDefinitionRepository_FindOrCreate_HashMismatchRejected(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	_, err := repo.FindOrCreate(ctx, "immutable_saga", 1, "def v1(): pass", nil)
	require.NoError(t, err)

	// Same (name, version) but different script content must error out.
	_, err = repo.FindOrCreate(ctx, "immutable_saga", 1, "def v1_TAMPERED(): pass", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSagaDefinitionHashMismatch)

	// Confirm no second row was created.
	var count int64
	require.NoError(t, db.Model(&SagaDefinition{}).Where("name = ?", "immutable_saga").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestSagaDefinitionRepository_FindOrCreate_PersistsParamsSchema(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	params := JSONB{"amount": map[string]any{"type": "Decimal", "required": true}}

	def, err := repo.FindOrCreate(ctx, "schema_saga", 1, "def main(): pass", params)
	require.NoError(t, err)
	require.NotNil(t, def.ParamsSchema)

	loaded, err := repo.FindByID(ctx, def.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.ParamsSchema)
	assert.Contains(t, loaded.ParamsSchema, "amount")
}

func TestSagaDefinitionRepository_FindByID_Success(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	created, err := repo.FindOrCreate(ctx, "lookup_saga", 1, "def main(): pass", nil)
	require.NoError(t, err)

	loaded, err := repo.FindByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, created.ID, loaded.ID)
	assert.Equal(t, created.Name, loaded.Name)
	assert.Equal(t, created.Version, loaded.Version)
	assert.Equal(t, created.Script, loaded.Script)
	assert.Equal(t, created.ScriptHash, loaded.ScriptHash)
}

func TestSagaDefinitionRepository_FindByID_NotFound(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSagaDefinitionNotFound),
		"expected ErrSagaDefinitionNotFound, got %v", err)
}

func TestSagaDefinitionRepository_FindByID_NilUUIDReturnsNotFound(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.Nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSagaDefinitionNotFound)
}
