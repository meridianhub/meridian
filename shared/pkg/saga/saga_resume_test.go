package saga

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadPinnedDefinition_ReturnsPinnedDefinition is the central invariant:
// when a definition with the same name is replaced by a NEW row (simulating a
// manifest update mid-flight), LoadPinnedDefinition still returns the original
// row referenced by instance.SagaDefinitionID - never the new row.
func TestLoadPinnedDefinition_ReturnsPinnedDefinition(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	// Pin v1 of the saga and create an instance referencing it.
	v1Script := "def v1(): return 'old'"
	v1, err := repo.FindOrCreate(ctx, "drift_test", "1", v1Script, nil)
	require.NoError(t, err)

	instance := &SagaInstance{
		SagaDefinitionID:  v1.ID,
		SagaName:          "drift_test",
		SagaVersion:       1,
		ScriptHashAtStart: v1.ScriptHash,
		Status:            SagaStatusRunning,
	}

	// Simulate a manifest update: v2 is created with new script, but v1 stays around.
	v2Script := "def v2(): return 'new'"
	v2, err := repo.FindOrCreate(ctx, "drift_test", "2", v2Script, nil)
	require.NoError(t, err)
	require.NotEqual(t, v1.ID, v2.ID)

	// The resume path MUST return v1, even though v2 exists.
	loaded, err := LoadPinnedDefinition(ctx, repo, instance)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, v1.ID, loaded.ID, "must return the pinned definition, not the latest")
	assert.Equal(t, v1Script, loaded.Script, "must return the pinned script, not v2")
}

func TestLoadPinnedDefinition_NilInstanceRejected(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	_, err := LoadPinnedDefinition(context.Background(), repo, nil)
	require.Error(t, err)
}

func TestLoadPinnedDefinition_MissingDefinitionIDRejected(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	instance := &SagaInstance{
		SagaDefinitionID: uuid.Nil,
		Status:           SagaStatusRunning,
	}

	_, err := LoadPinnedDefinition(context.Background(), repo, instance)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstanceMissingDefinitionID)
}

func TestLoadPinnedDefinition_NotFoundReturnsWrappedError(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	instance := &SagaInstance{
		SagaDefinitionID: uuid.New(), // random; no matching row
		Status:           SagaStatusRunning,
	}

	_, err := LoadPinnedDefinition(context.Background(), repo, instance)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSagaDefinitionNotFound),
		"expected wrapped ErrSagaDefinitionNotFound, got %v", err)
}

func TestLoadPinnedDefinition_HashMismatchRejected(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	v1Script := "def v1(): return 'pinned'"
	v1, err := repo.FindOrCreate(ctx, "tamper_test", "1", v1Script, nil)
	require.NoError(t, err)

	// Build an instance with a stale/tampered hash recorded.
	instance := &SagaInstance{
		SagaDefinitionID:  v1.ID,
		ScriptHashAtStart: "deadbeef" + v1.ScriptHash[8:], // mutated prefix
		Status:            SagaStatusRunning,
	}

	_, err = LoadPinnedDefinition(ctx, repo, instance)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScriptHashCorruption)
}

func TestLoadPinnedDefinition_EmptyHashSkipsVerification(t *testing.T) {
	// Legacy instances created before ScriptHashAtStart was populated must still
	// resume - we treat empty hash as "skip verification".
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	repo := NewSagaDefinitionRepository(db)
	ctx := context.Background()

	def, err := repo.FindOrCreate(ctx, "legacy_test", "1", "def main(): pass", nil)
	require.NoError(t, err)

	instance := &SagaInstance{
		SagaDefinitionID:  def.ID,
		ScriptHashAtStart: "", // legacy
		Status:            SagaStatusRunning,
	}

	loaded, err := LoadPinnedDefinition(ctx, repo, instance)
	require.NoError(t, err)
	assert.Equal(t, def.ID, loaded.ID)
}
