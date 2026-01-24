package saga

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCausationTree_SingleSaga tests retrieving a causation tree for a single saga without children.
func TestGetCausationTree_SingleSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a single saga instance
	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "test_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 2,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Create step results
	for i := 0; i < 2; i++ {
		stepResult := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: sagaID,
			StepIndex:      i,
			StepName:       "step_" + string(rune('0'+i)),
			IdempotencyKey: FormatIdempotencyKey(sagaID, i),
			Status:         StepStatusCompleted,
		}
		err = db.Create(stepResult).Error
		require.NoError(t, err)
	}

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), sagaID)
	require.NoError(t, err)

	assert.Equal(t, sagaID, tree.SagaID)
	assert.Equal(t, "test_saga", tree.SagaName)
	assert.Equal(t, string(SagaStatusCompleted), tree.Status)
	assert.Len(t, tree.Steps, 2)
	assert.Empty(t, tree.Steps[0].ChildSagas)
	assert.Empty(t, tree.Steps[1].ChildSagas)
}

// TestGetCausationTree_ParentWithChildren tests retrieving a causation tree with parent-child relationships.
func TestGetCausationTree_ParentWithChildren(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create parent saga
	parentID := uuid.New()
	parent := &SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "parent_saga",
		Status:           SagaStatusRunning,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 2,
	}
	err = db.Create(parent).Error
	require.NoError(t, err)

	// Create parent step results (validate, split)
	stepNames := []string{"validate", "split"}
	for i, name := range stepNames {
		stepResult := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: parentID,
			StepIndex:      i,
			StepName:       name,
			IdempotencyKey: FormatIdempotencyKey(parentID, i),
			Status:         StepStatusCompleted,
		}
		err = db.Create(stepResult).Error
		require.NoError(t, err)
	}

	// Create child sagas spawned from step 1 (split)
	childNames := []string{"child_saga_1", "child_saga_2", "child_saga_3"}
	childStatuses := []SagaStatus{SagaStatusCompleted, SagaStatusFailed, SagaStatusRunning}
	parentStepIndex := 1

	for i, name := range childNames {
		childID := uuid.New()
		child := &SagaInstance{
			ID:               childID,
			SagaDefinitionID: uuid.New(),
			SagaName:         name,
			Status:           childStatuses[i],
			CorrelationID:    parent.CorrelationID,
			ParentSagaID:     &parentID,
			ParentStepIndex:  &parentStepIndex,
			CurrentStepIndex: 1,
		}
		err = db.Create(child).Error
		require.NoError(t, err)

		// Create a step result for each child
		childStep := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: childID,
			StepIndex:      0,
			StepName:       "process",
			IdempotencyKey: FormatIdempotencyKey(childID, 0),
			Status:         StepStatusCompleted,
		}
		err = db.Create(childStep).Error
		require.NoError(t, err)
	}

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), parentID)
	require.NoError(t, err)

	assert.Equal(t, parentID, tree.SagaID)
	assert.Equal(t, "parent_saga", tree.SagaName)
	assert.Len(t, tree.Steps, 2)

	// Step 0 (validate) should have no children
	assert.Equal(t, "validate", tree.Steps[0].Name)
	assert.Empty(t, tree.Steps[0].ChildSagas)

	// Step 1 (split) should have 3 children
	assert.Equal(t, "split", tree.Steps[1].Name)
	assert.Len(t, tree.Steps[1].ChildSagas, 3)

	// Verify child sagas
	childSagas := tree.Steps[1].ChildSagas
	sagaNames := make([]string, len(childSagas))
	for i, child := range childSagas {
		sagaNames[i] = child.SagaName
	}
	assert.ElementsMatch(t, childNames, sagaNames)
}

// TestGetCausationTree_ThreeLevels tests a three-level causation tree (parent -> child -> grandchild).
func TestGetCausationTree_ThreeLevels(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	correlationID := uuid.New()

	// Create grandparent saga
	grandparentID := uuid.New()
	grandparent := &SagaInstance{
		ID:               grandparentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "grandparent_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 1,
	}
	err = db.Create(grandparent).Error
	require.NoError(t, err)

	// Create grandparent step
	grandparentStep := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: grandparentID,
		StepIndex:      0,
		StepName:       "spawn_child",
		IdempotencyKey: FormatIdempotencyKey(grandparentID, 0),
		Status:         StepStatusCompleted,
	}
	err = db.Create(grandparentStep).Error
	require.NoError(t, err)

	// Create parent saga (child of grandparent)
	parentID := uuid.New()
	parentStepIdx := 0
	parent := &SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "parent_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &grandparentID,
		ParentStepIndex:  &parentStepIdx,
		CurrentStepIndex: 1,
	}
	err = db.Create(parent).Error
	require.NoError(t, err)

	// Create parent step
	parentStep := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: parentID,
		StepIndex:      0,
		StepName:       "spawn_grandchild",
		IdempotencyKey: FormatIdempotencyKey(parentID, 0),
		Status:         StepStatusCompleted,
	}
	err = db.Create(parentStep).Error
	require.NoError(t, err)

	// Create child saga (grandchild of grandparent)
	childID := uuid.New()
	childStepIdx := 0
	child := &SagaInstance{
		ID:               childID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "child_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &parentID,
		ParentStepIndex:  &childStepIdx,
		CurrentStepIndex: 1,
	}
	err = db.Create(child).Error
	require.NoError(t, err)

	// Create child step
	childStep := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: childID,
		StepIndex:      0,
		StepName:       "process",
		IdempotencyKey: FormatIdempotencyKey(childID, 0),
		Status:         StepStatusCompleted,
	}
	err = db.Create(childStep).Error
	require.NoError(t, err)

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), grandparentID)
	require.NoError(t, err)

	// Verify three-level hierarchy
	assert.Equal(t, grandparentID, tree.SagaID)
	assert.Equal(t, "grandparent_saga", tree.SagaName)
	require.Len(t, tree.Steps, 1)
	require.Len(t, tree.Steps[0].ChildSagas, 1)

	parentNode := tree.Steps[0].ChildSagas[0]
	assert.Equal(t, parentID, parentNode.SagaID)
	assert.Equal(t, "parent_saga", parentNode.SagaName)
	require.Len(t, parentNode.Steps, 1)
	require.Len(t, parentNode.Steps[0].ChildSagas, 1)

	childNode := parentNode.Steps[0].ChildSagas[0]
	assert.Equal(t, childID, childNode.SagaID)
	assert.Equal(t, "child_saga", childNode.SagaName)

	// Verify depth
	depth, err := repo.GetTreeDepth(context.Background(), grandparentID)
	require.NoError(t, err)
	assert.Equal(t, 3, depth)
}

// TestGetCausationTree_FailedStepInfo tests that failed step information is included in the tree.
func TestGetCausationTree_FailedStepInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a failed saga
	sagaID := uuid.New()
	failedStepIdx := 2
	errorMsg := "Insufficient balance: required 1000.00, available 500.00"
	errorCat := string(ErrorCategoryFatal)

	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "payment_saga",
		Status:           SagaStatusFailed,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 2,
		FailedStepIndex:  &failedStepIdx,
		ErrorMessage:     &errorMsg,
		ErrorCategory:    &errorCat,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Create step results including the failed one
	steps := []struct {
		index  int
		name   string
		status StepStatus
		err    *string
	}{
		{0, "validate", StepStatusCompleted, nil},
		{1, "reserve", StepStatusCompleted, nil},
		{2, "debit", StepStatusFailed, &errorMsg},
	}

	for _, s := range steps {
		stepResult := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: sagaID,
			StepIndex:      s.index,
			StepName:       s.name,
			IdempotencyKey: FormatIdempotencyKey(sagaID, s.index),
			Status:         s.status,
			Error:          s.err,
		}
		err = db.Create(stepResult).Error
		require.NoError(t, err)
	}

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), sagaID)
	require.NoError(t, err)

	assert.Equal(t, string(SagaStatusFailed), tree.Status)
	require.NotNil(t, tree.FailedStep)
	assert.Equal(t, 2, tree.FailedStep.Index)
	assert.Contains(t, tree.FailedStep.Error, "Insufficient balance")
	assert.Equal(t, "FATAL", tree.FailedStep.ErrorCategory)

	// Also verify the step's error is populated
	require.Len(t, tree.Steps, 3)
	assert.Equal(t, string(StepStatusFailed), tree.Steps[2].Status)
	require.NotNil(t, tree.Steps[2].Error)
	assert.Contains(t, *tree.Steps[2].Error, "Insufficient balance")
}

// TestGetCausationTree_NotFound tests that ErrSagaNotFound is returned for non-existent sagas.
func TestGetCausationTree_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	repo := NewCausationTreeRepository(db)
	_, err = repo.GetCausationTree(context.Background(), uuid.New())

	assert.ErrorIs(t, err, ErrSagaNotFound)
}

// TestGetCausationTree_BiTemporalFields tests that bi-temporal fields are properly populated.
func TestGetCausationTree_BiTemporalFields(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga with bi-temporal fields
	sagaID := uuid.New()
	knowledgeAt := time.Now().Add(-24 * time.Hour)

	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "bi_temporal_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		KnowledgeAt:      &knowledgeAt,
		CurrentStepIndex: 1,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Create a step result
	stepResult := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "process",
		IdempotencyKey: FormatIdempotencyKey(sagaID, 0),
		Status:         StepStatusCompleted,
	}
	err = db.Create(stepResult).Error
	require.NoError(t, err)

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), sagaID)
	require.NoError(t, err)

	require.NotNil(t, tree.KnowledgeAt)
	// Truncate to second precision for comparison (database may lose some precision)
	assert.WithinDuration(t, knowledgeAt, *tree.KnowledgeAt, time.Second)
}

// TestGetTreeDepth tests the GetTreeDepth method.
func TestGetTreeDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	correlationID := uuid.New()

	// Create a chain of sagas with depth 5
	var prevID *uuid.UUID
	for depth := 1; depth <= 5; depth++ {
		sagaID := uuid.New()
		var parentStepIdx *int
		if prevID != nil {
			idx := 0
			parentStepIdx = &idx
		}

		instance := &SagaInstance{
			ID:               sagaID,
			SagaDefinitionID: uuid.New(),
			SagaName:         "saga_depth_" + string(rune('0'+depth)),
			Status:           SagaStatusCompleted,
			CorrelationID:    correlationID,
			ParentSagaID:     prevID,
			ParentStepIndex:  parentStepIdx,
			CurrentStepIndex: 1,
		}
		err = db.Create(instance).Error
		require.NoError(t, err)

		// Create step result if not root
		if prevID != nil {
			stepResult := &SagaStepResult{
				ID:             uuid.New(),
				SagaInstanceID: *prevID,
				StepIndex:      0,
				StepName:       "spawn",
				IdempotencyKey: FormatIdempotencyKey(*prevID, 0),
				Status:         StepStatusCompleted,
			}
			err = db.Create(stepResult).Error
			require.NoError(t, err)
		}

		prevID = &sagaID
	}

	// Get the root saga ID (first created)
	var rootID uuid.UUID
	err = db.Model(&SagaInstance{}).
		Where("parent_saga_id IS NULL AND correlation_id = ?", correlationID).
		Select("id").
		Scan(&rootID).Error
	require.NoError(t, err)

	repo := NewCausationTreeRepository(db)
	depth, err := repo.GetTreeDepth(context.Background(), rootID)
	require.NoError(t, err)
	assert.Equal(t, 5, depth)
}

// TestGetCausationTree_SagaWithNoSteps tests a saga that has no step results yet.
func TestGetCausationTree_SagaWithNoSteps(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a saga with no step results (just started)
	sagaID := uuid.New()
	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "pending_saga",
		Status:           SagaStatusPending,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 0,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), sagaID)
	require.NoError(t, err)

	assert.Equal(t, sagaID, tree.SagaID)
	assert.Equal(t, "pending_saga", tree.SagaName)
	assert.Equal(t, string(SagaStatusPending), tree.Status)
	assert.Empty(t, tree.Steps)
}

// TestGetCausationTree_ChildWithoutParentStep tests a child saga when parent step has no result.
// This can happen if parent is mid-execution when child is spawned.
func TestGetCausationTree_ChildWithoutParentStep(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create parent saga with no step results
	parentID := uuid.New()
	parent := &SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "parent_saga",
		Status:           SagaStatusRunning,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 1,
	}
	err = db.Create(parent).Error
	require.NoError(t, err)

	// Create child saga linked to parent step 0 (which has no step result)
	childID := uuid.New()
	childStepIdx := 0
	child := &SagaInstance{
		ID:               childID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "child_saga",
		Status:           SagaStatusRunning,
		CorrelationID:    parent.CorrelationID,
		ParentSagaID:     &parentID,
		ParentStepIndex:  &childStepIdx,
		CurrentStepIndex: 0,
	}
	err = db.Create(child).Error
	require.NoError(t, err)

	// Test the causation tree repository
	repo := NewCausationTreeRepository(db)
	tree, err := repo.GetCausationTree(context.Background(), parentID)
	require.NoError(t, err)

	assert.Equal(t, parentID, tree.SagaID)
	// Parent should have a placeholder step created for the child
	require.Len(t, tree.Steps, 1)
	assert.Equal(t, 0, tree.Steps[0].Index)
	assert.Empty(t, tree.Steps[0].Name) // Placeholder has no name
	require.Len(t, tree.Steps[0].ChildSagas, 1)
	assert.Equal(t, childID, tree.Steps[0].ChildSagas[0].SagaID)
}
