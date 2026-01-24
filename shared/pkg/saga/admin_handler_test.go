package saga

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
)

// TestAdminHandler_GetCausationTree_InvalidUUID tests that invalid UUID returns InvalidArgument.
func TestAdminHandler_GetCausationTree_InvalidUUID(t *testing.T) {
	// Create handler with nil repo (won't be reached)
	handler := NewAdminHandler(nil, nil)

	req := &sagav1.GetCausationTreeRequest{
		SagaId: "not-a-valid-uuid",
	}

	_, err := handler.GetCausationTree(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid saga_id")
}

// TestAdminHandler_GetCausationTree_NotFound tests that missing saga returns NotFound.
func TestAdminHandler_GetCausationTree_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	repo := NewCausationTreeRepository(db)
	handler := NewAdminHandler(repo, nil)

	req := &sagav1.GetCausationTreeRequest{
		SagaId: uuid.New().String(),
	}

	_, err = handler.GetCausationTree(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "saga not found")
}

// TestAdminHandler_GetCausationTree_Success tests successful causation tree retrieval.
func TestAdminHandler_GetCausationTree_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create test data
	parentID := uuid.New()
	knowledgeAt := time.Now().Add(-1 * time.Hour)

	parent := &SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "test_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		KnowledgeAt:      &knowledgeAt,
		CurrentStepIndex: 2,
	}
	err = db.Create(parent).Error
	require.NoError(t, err)

	// Create step results
	for i := 0; i < 2; i++ {
		stepResult := &SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: parentID,
			StepIndex:      i,
			StepName:       "step_" + string(rune('0'+i)),
			IdempotencyKey: FormatIdempotencyKey(parentID, i),
			Status:         StepStatusCompleted,
		}
		err = db.Create(stepResult).Error
		require.NoError(t, err)
	}

	// Create child saga
	childID := uuid.New()
	childStepIdx := 1
	child := &SagaInstance{
		ID:               childID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "child_saga",
		Status:           SagaStatusCompleted,
		CorrelationID:    parent.CorrelationID,
		ParentSagaID:     &parentID,
		ParentStepIndex:  &childStepIdx,
		CurrentStepIndex: 1,
	}
	err = db.Create(child).Error
	require.NoError(t, err)

	repo := NewCausationTreeRepository(db)
	handler := NewAdminHandler(repo, nil)

	req := &sagav1.GetCausationTreeRequest{
		SagaId: parentID.String(),
	}

	resp, err := handler.GetCausationTree(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Tree)

	// Verify proto conversion
	assert.Equal(t, parentID.String(), resp.Tree.SagaId)
	assert.Equal(t, "test_saga", resp.Tree.SagaName)
	assert.Equal(t, string(SagaStatusCompleted), resp.Tree.Status)
	assert.Len(t, resp.Tree.Steps, 2)

	// Verify bi-temporal field conversion
	require.NotNil(t, resp.Tree.KnowledgeAt)
	assert.WithinDuration(t, knowledgeAt, resp.Tree.KnowledgeAt.AsTime(), time.Second)

	// Verify child saga is nested under step 1
	require.Len(t, resp.Tree.Steps[1].ChildSagas, 1)
	assert.Equal(t, childID.String(), resp.Tree.Steps[1].ChildSagas[0].SagaId)
	assert.Equal(t, "child_saga", resp.Tree.Steps[1].ChildSagas[0].SagaName)

	// Verify depth is returned
	assert.Equal(t, int32(2), resp.Depth)
}

// TestAdminHandler_GetCausationTree_FailedStepConversion tests that failed step info is converted correctly.
func TestAdminHandler_GetCausationTree_FailedStepConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db, cleanup := setupTestPostgres(t)
	defer cleanup()

	err := RunSagaMigrations(db)
	require.NoError(t, err)

	// Create a failed saga
	sagaID := uuid.New()
	failedStepIdx := 1
	errorMsg := "Database connection timeout"
	errorCat := string(ErrorCategoryTransient)

	instance := &SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "failing_saga",
		Status:           SagaStatusFailed,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 1,
		FailedStepIndex:  &failedStepIdx,
		ErrorMessage:     &errorMsg,
		ErrorCategory:    &errorCat,
	}
	err = db.Create(instance).Error
	require.NoError(t, err)

	repo := NewCausationTreeRepository(db)
	handler := NewAdminHandler(repo, nil)

	req := &sagav1.GetCausationTreeRequest{
		SagaId: sagaID.String(),
	}

	resp, err := handler.GetCausationTree(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tree.FailedStep)

	assert.Equal(t, int32(1), resp.Tree.FailedStep.Index)
	assert.Equal(t, "Database connection timeout", resp.Tree.FailedStep.Error)
	assert.Equal(t, "TRANSIENT", resp.Tree.FailedStep.ErrorCategory)
}
