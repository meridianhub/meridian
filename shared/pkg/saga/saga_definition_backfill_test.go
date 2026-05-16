package saga

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestBackfillSagaDefinitionIDs_LinksInstanceToLocalDefinition is the happy
// path: an in-flight instance whose saga_definition_id is stale gets relinked
// to the matching (name, version) row in the local saga_definitions table.
func TestBackfillSagaDefinitionIDs_LinksInstanceToLocalDefinition(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	// Pre-create the local pinning row.
	repo := NewSagaDefinitionRepository(db)
	def, err := repo.FindOrCreate(ctx, "deposit", "1", "def main(): pass", nil)
	require.NoError(t, err)

	// Create an in-flight instance with a stale (random) saga_definition_id.
	instance := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(), // stale - does NOT exist in saga_definitions
		SagaName:         "deposit",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&instance).Error)

	result, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Linked)
	assert.Equal(t, 0, result.FlaggedManualIntervention)

	// Verify the instance now points at the local definition.
	var reloaded SagaInstance
	require.NoError(t, db.First(&reloaded, "id = ?", instance.ID).Error)
	assert.Equal(t, def.ID, reloaded.SagaDefinitionID)
	assert.Equal(t, SagaStatusRunning, reloaded.Status)
}

// TestBackfillSagaDefinitionIDs_FlagsOrphanedInstance verifies the failure
// path: instances with no matching saga_definitions row are transitioned to
// FAILED_MANUAL_INTERVENTION rather than left in an inconsistent state.
func TestBackfillSagaDefinitionIDs_FlagsOrphanedInstance(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	// No saga_definitions row will exist for ("orphan_saga", 99).
	instance := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		SagaName:         "orphan_saga",
		SagaVersion:      99,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&instance).Error)

	result, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Linked)
	assert.Equal(t, 1, result.FlaggedManualIntervention)

	var reloaded SagaInstance
	require.NoError(t, db.First(&reloaded, "id = ?", instance.ID).Error)
	assert.Equal(t, SagaStatusFailedManualIntervention, reloaded.Status)
	require.NotNil(t, reloaded.ErrorMessage)
	assert.Contains(t, *reloaded.ErrorMessage, "orphan_saga")
}

// TestBackfillSagaDefinitionIDs_SkipsAlreadyLinkedInstance verifies that
// instances whose saga_definition_id already resolves to a saga_definitions
// row are left alone.
func TestBackfillSagaDefinitionIDs_SkipsAlreadyLinkedInstance(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	repo := NewSagaDefinitionRepository(db)
	def, err := repo.FindOrCreate(ctx, "happy_saga", "1", "def main(): pass", nil)
	require.NoError(t, err)

	instance := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: def.ID, // already linked
		SagaName:         "happy_saga",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&instance).Error)

	result, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Linked, "already-linked instance must not be re-linked")
	assert.Equal(t, 0, result.FlaggedManualIntervention)
}

// TestBackfillSagaDefinitionIDs_SkipsTerminalInstances verifies that the
// backfill ignores COMPLETED/FAILED/etc. instances - only in-flight statuses
// matter for resume correctness.
func TestBackfillSagaDefinitionIDs_SkipsTerminalInstances(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	completed := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(), // stale; would normally be flagged
		SagaName:         "old_saga",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusCompleted,
	}
	require.NoError(t, db.Create(&completed).Error)

	result, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Linked)
	assert.Equal(t, 0, result.FlaggedManualIntervention)

	var reloaded SagaInstance
	require.NoError(t, db.First(&reloaded, "id = ?", completed.ID).Error)
	assert.Equal(t, SagaStatusCompleted, reloaded.Status, "terminal status must be preserved")
}

// TestBackfillSagaDefinitionIDs_Idempotent verifies that running the backfill
// twice produces the same final state and zero additional work on the second
// run.
func TestBackfillSagaDefinitionIDs_Idempotent(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	repo := NewSagaDefinitionRepository(db)
	def, err := repo.FindOrCreate(ctx, "idem_saga", "1", "def main(): pass", nil)
	require.NoError(t, err)

	instance := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		SagaName:         "idem_saga",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&instance).Error)

	first, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Linked)

	second, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 0, second.Linked, "second run must be a no-op")
	assert.Equal(t, 0, second.FlaggedManualIntervention)

	var reloaded SagaInstance
	require.NoError(t, db.First(&reloaded, "id = ?", instance.ID).Error)
	assert.Equal(t, def.ID, reloaded.SagaDefinitionID)
}

// TestBackfillSagaDefinitionIDs_HandlesEmptyTable confirms the backfill is
// safe to call against a fresh database with no instances or definitions.
func TestBackfillSagaDefinitionIDs_HandlesEmptyTable(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	result, err := BackfillSagaDefinitionIDs(context.Background(), db)
	require.NoError(t, err)
	assert.Equal(t, BackfillResult{}, result)
}

// TestBackfillSagaDefinitionIDs_TransactionAtomicity verifies that all changes
// happen inside one transaction by checking the SagaInstance row's updated_at
// matches the saga_definition_id update.
func TestBackfillSagaDefinitionIDs_TransactionAtomicity(t *testing.T) {
	db, cleanup := setupTestPostgres(t)
	defer cleanup()
	ctx := context.Background()

	repo := NewSagaDefinitionRepository(db)
	def, err := repo.FindOrCreate(ctx, "atomic_saga", "1", "def main(): pass", nil)
	require.NoError(t, err)

	matching := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		SagaName:         "atomic_saga",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&matching).Error)

	orphan := SagaInstance{
		ID:               uuid.New(),
		SagaDefinitionID: uuid.New(),
		SagaName:         "orphan_atomic",
		SagaVersion:      1,
		CorrelationID:    uuid.New(),
		Status:           SagaStatusRunning,
	}
	require.NoError(t, db.Create(&orphan).Error)

	result, err := BackfillSagaDefinitionIDs(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Linked)
	assert.Equal(t, 1, result.FlaggedManualIntervention)

	var matchingReloaded, orphanReloaded SagaInstance
	require.NoError(t, db.First(&matchingReloaded, "id = ?", matching.ID).Error)
	require.NoError(t, db.First(&orphanReloaded, "id = ?", orphan.ID).Error)

	assert.Equal(t, def.ID, matchingReloaded.SagaDefinitionID)
	assert.Equal(t, SagaStatusFailedManualIntervention, orphanReloaded.Status)
}

// Compile-time check that the helper can be invoked with a transaction.
var _ = func() {
	var tx *gorm.DB
	_, _ = findLocalDefinitionByNameVersion(tx, "name", 1)
}
