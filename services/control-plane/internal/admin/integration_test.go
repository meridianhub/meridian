package admin

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
)

var (
	sharedDB      *gorm.DB
	sharedOnce    sync.Once
	sharedInitErr error
	sharedCleanup func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedCleanup != nil {
		sharedCleanup()
	}
	os.Exit(code)
}

func initSharedContainer() error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	crdbContainer, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	connConfig, err := crdbContainer.ConnectionConfig(ctx)
	if err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("connection config: %w", err)
	}

	db, err := gorm.Open(gormpg.Open(connConfig.ConnString()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("gorm open: %w", err)
	}

	if err := saga.RunSagaMigrations(db); err != nil {
		_ = crdbContainer.Terminate(ctx)
		return fmt.Errorf("migrations: %w", err)
	}

	sharedDB = db
	sharedCleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = crdbContainer.Terminate(cleanupCtx)
	}

	return nil
}

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	sharedOnce.Do(func() {
		sharedInitErr = initSharedContainer()
	})
	if sharedInitErr != nil {
		t.Fatalf("shared CockroachDB setup failed: %v", sharedInitErr)
	}

	// Clean tables before each test
	require.NoError(t, sharedDB.Exec("DELETE FROM saga_step_results").Error)
	require.NoError(t, sharedDB.Exec("DELETE FROM saga_instances").Error)

	return sharedDB
}

// TestIntegration_GetCausationTreeForTransaction_ViaSagaCorrelation tests tracing
// a transaction that matches a saga's correlation_id.
func TestIntegration_GetCausationTreeForTransaction_ViaSagaCorrelation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	// Create a saga instance with a specific correlation_id
	sagaID := uuid.New()
	correlationID := uuid.New()
	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "process_payment",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 2,
	}
	require.NoError(t, db.Create(instance).Error)

	// Create step results
	for i := 0; i < 2; i++ {
		step := &saga.SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: sagaID,
			StepIndex:      i,
			StepName:       fmt.Sprintf("step_%d", i),
			IdempotencyKey: saga.FormatIdempotencyKey(sagaID, i),
			Status:         saga.StepStatusCompleted,
		}
		require.NoError(t, db.Create(step).Error)
	}

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	// Trace using the correlation_id as transaction_id
	req := &controlplanev1.GetCausationTreeForTransactionRequest{
		TransactionId: correlationID.String(),
	}

	resp, err := handler.GetCausationTreeForTransaction(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Tree)

	assert.Equal(t, sagaID.String(), resp.Tree.SagaId)
	assert.Equal(t, "process_payment", resp.Tree.SagaName)
	assert.Equal(t, string(saga.SagaStatusCompleted), resp.Tree.Status)
	assert.Len(t, resp.Tree.Steps, 2)
	assert.Equal(t, correlationID.String(), resp.TransactionId)
	assert.Equal(t, sagaID.String(), resp.SagaId)
}

// TestIntegration_GetCausationTreeForTransaction_NotFound tests that a missing
// transaction returns NotFound.
func TestIntegration_GetCausationTreeForTransaction_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	req := &controlplanev1.GetCausationTreeForTransactionRequest{
		TransactionId: uuid.New().String(),
	}

	_, err := handler.GetCausationTreeForTransaction(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestIntegration_GetCausationTreeForEvent_ViaCausationID tests tracing
// an event via its causation_id matching a saga step result.
func TestIntegration_GetCausationTreeForEvent_ViaCausationID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	sagaID := uuid.New()
	causationID := saga.GenerateCausationID(sagaID, 0)

	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "energy_settlement",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    uuid.New(),
		CurrentStepIndex: 1,
	}
	require.NoError(t, db.Create(instance).Error)

	step := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "validate_meter_read",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 0),
		Status:         saga.StepStatusCompleted,
		CausationID:    &causationID,
	}
	require.NoError(t, db.Create(step).Error)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	// Trace using the causation_id as event_id
	req := &controlplanev1.GetCausationTreeForEventRequest{
		EventId: causationID.String(),
	}

	resp, err := handler.GetCausationTreeForEvent(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Tree)

	assert.Equal(t, sagaID.String(), resp.Tree.SagaId)
	assert.Equal(t, "energy_settlement", resp.Tree.SagaName)
	assert.Equal(t, causationID.String(), resp.EventId)
	assert.Equal(t, sagaID.String(), resp.SagaId)
}

// TestIntegration_GetCausationTreeForEvent_NotFound tests that an unknown
// event returns NotFound.
func TestIntegration_GetCausationTreeForEvent_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	req := &controlplanev1.GetCausationTreeForEventRequest{
		EventId: uuid.New().String(),
	}

	_, err := handler.GetCausationTreeForEvent(context.Background(), req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestIntegration_GetCausationTreeForTransaction_WithChildSagas tests that
// the tree includes child sagas nested under the correct parent step.
func TestIntegration_GetCausationTreeForTransaction_WithChildSagas(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	// Create parent saga
	parentID := uuid.New()
	correlationID := uuid.New()
	parent := &saga.SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "payment_orchestrator",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 2,
	}
	require.NoError(t, db.Create(parent).Error)

	// Parent steps
	for i := 0; i < 2; i++ {
		step := &saga.SagaStepResult{
			ID:             uuid.New(),
			SagaInstanceID: parentID,
			StepIndex:      i,
			StepName:       fmt.Sprintf("parent_step_%d", i),
			IdempotencyKey: saga.FormatIdempotencyKey(parentID, i),
			Status:         saga.StepStatusCompleted,
		}
		require.NoError(t, db.Create(step).Error)
	}

	// Create child saga spawned from step 1
	childID := uuid.New()
	childStepIdx := 1
	child := &saga.SagaInstance{
		ID:               childID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "charge_card",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &parentID,
		ParentStepIndex:  &childStepIdx,
		CurrentStepIndex: 1,
	}
	require.NoError(t, db.Create(child).Error)

	childStep := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: childID,
		StepIndex:      0,
		StepName:       "process_charge",
		IdempotencyKey: saga.FormatIdempotencyKey(childID, 0),
		Status:         saga.StepStatusCompleted,
	}
	require.NoError(t, db.Create(childStep).Error)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	req := &controlplanev1.GetCausationTreeForTransactionRequest{
		TransactionId: correlationID.String(),
	}

	resp, err := handler.GetCausationTreeForTransaction(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tree)

	// Verify parent
	assert.Equal(t, parentID.String(), resp.Tree.SagaId)
	assert.Equal(t, "payment_orchestrator", resp.Tree.SagaName)
	assert.Len(t, resp.Tree.Steps, 2)

	// Verify child is nested under step 1
	require.Len(t, resp.Tree.Steps[1].ChildSagas, 1)
	assert.Equal(t, childID.String(), resp.Tree.Steps[1].ChildSagas[0].SagaId)
	assert.Equal(t, "charge_card", resp.Tree.Steps[1].ChildSagas[0].SagaName)
	assert.Len(t, resp.Tree.Steps[1].ChildSagas[0].Steps, 1)

	// Verify depth
	assert.Equal(t, int32(2), resp.Depth)
}

// TestIntegration_FindRootSaga_WalksUpChain tests that the root saga finder
// correctly walks up the parent chain.
func TestIntegration_FindRootSaga_WalksUpChain(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	// Create 3-level chain: grandparent -> parent -> child
	grandparentID := uuid.New()
	parentID := uuid.New()
	childID := uuid.New()
	correlationID := uuid.New()

	grandparent := &saga.SagaInstance{
		ID:               grandparentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "grandparent",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		CurrentStepIndex: 1,
	}
	require.NoError(t, db.Create(grandparent).Error)

	step0 := 0
	parent := &saga.SagaInstance{
		ID:               parentID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "parent",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &grandparentID,
		ParentStepIndex:  &step0,
		CurrentStepIndex: 1,
	}
	require.NoError(t, db.Create(parent).Error)

	child := &saga.SagaInstance{
		ID:               childID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "child",
		Status:           saga.SagaStatusCompleted,
		CorrelationID:    correlationID,
		ParentSagaID:     &parentID,
		ParentStepIndex:  &step0,
		CurrentStepIndex: 0,
	}
	require.NoError(t, db.Create(child).Error)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)

	// When we query by the child's correlation_id, we should get the grandparent as root
	result, err := visualizer.GetCausationTreeForTransaction(context.Background(), correlationID)
	require.NoError(t, err)
	require.NotNil(t, result)

	// The root should be the grandparent (first saga created with this correlation_id)
	assert.Equal(t, grandparentID, result.SagaID)
}

// TestIntegration_GetCausationTreeForTransaction_FailedSagaWithStepDetails tests
// that failed sagas include error information in the response.
func TestIntegration_GetCausationTreeForTransaction_FailedSagaWithStepDetails(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := setupTestDB(t)

	sagaID := uuid.New()
	correlationID := uuid.New()
	failedIdx := 1
	errMsg := "insufficient funds"
	errCat := string(saga.ErrorCategoryFatal)

	instance := &saga.SagaInstance{
		ID:               sagaID,
		SagaDefinitionID: uuid.New(),
		SagaName:         "transfer_funds",
		Status:           saga.SagaStatusFailed,
		CorrelationID:    correlationID,
		CurrentStepIndex: 1,
		FailedStepIndex:  &failedIdx,
		ErrorMessage:     &errMsg,
		ErrorCategory:    &errCat,
	}
	require.NoError(t, db.Create(instance).Error)

	// Step 0 succeeded, step 1 failed
	step0 := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "validate",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 0),
		Status:         saga.StepStatusCompleted,
	}
	require.NoError(t, db.Create(step0).Error)

	stepErr := "insufficient funds"
	step1 := &saga.SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      1,
		StepName:       "debit",
		IdempotencyKey: saga.FormatIdempotencyKey(sagaID, 1),
		Status:         saga.StepStatusFailed,
		Error:          &stepErr,
	}
	require.NoError(t, db.Create(step1).Error)

	treeRepo := saga.NewCausationTreeRepository(db)
	visualizer := NewCausationVisualizer(db, treeRepo, nil)
	handler := NewHandler(visualizer, nil)

	req := &controlplanev1.GetCausationTreeForTransactionRequest{
		TransactionId: correlationID.String(),
	}

	resp, err := handler.GetCausationTreeForTransaction(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tree)

	assert.Equal(t, string(saga.SagaStatusFailed), resp.Tree.Status)

	// Verify failed step info
	require.NotNil(t, resp.Tree.FailedStep)
	assert.Equal(t, int32(1), resp.Tree.FailedStep.Index)
	assert.Equal(t, "insufficient funds", resp.Tree.FailedStep.Error)
	assert.Equal(t, "FATAL", resp.Tree.FailedStep.ErrorCategory)

	// Verify step error detail
	require.Len(t, resp.Tree.Steps, 2)
	assert.Equal(t, "FAILED", resp.Tree.Steps[1].Status)
	assert.Equal(t, "insufficient funds", resp.Tree.Steps[1].Error)
}
