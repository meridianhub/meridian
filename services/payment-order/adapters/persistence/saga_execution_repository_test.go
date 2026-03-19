package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSagaExecutionRepository_PersistExecution(t *testing.T) {
	db, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&sagaExecutionEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewSagaExecutionRepository(db)

	now := time.Now().UTC()
	completedAt := now.Add(500 * time.Millisecond)
	exec := &domain.SagaExecution{
		ID:             uuid.New(),
		PaymentOrderID: uuid.New(),
		SagaName:       "initiate-payment",
		SagaVersion:    1,
		Status:         domain.SagaExecutionStatusCompleted,
		CorrelationID:  "corr-123",
		Input:          map[string]any{"amount": 1000},
		Output:         map[string]any{"status": "ok"},
		ErrorMessage:   "",
		StepCount:      3,
		DurationMs:     500,
		StartedAt:      now,
		CompletedAt:    &completedAt,
	}

	err := repo.PersistExecution(ctx, exec)
	require.NoError(t, err)
}

func TestSagaExecutionRepository_PersistExecution_NilMaps(t *testing.T) {
	db, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&sagaExecutionEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewSagaExecutionRepository(db)

	exec := &domain.SagaExecution{
		ID:             uuid.New(),
		PaymentOrderID: uuid.New(),
		SagaName:       "initiate-payment",
		SagaVersion:    1,
		Status:         domain.SagaExecutionStatusRunning,
		CorrelationID:  "corr-456",
		Input:          nil, // nil maps should be normalized to empty objects
		Output:         nil,
		StartedAt:      time.Now().UTC(),
	}

	err := repo.PersistExecution(ctx, exec)
	require.NoError(t, err)
}

func TestSagaExecutionRepository_PersistExecution_NilExecution(t *testing.T) {
	db, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&sagaExecutionEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewSagaExecutionRepository(db)

	err := repo.PersistExecution(context.TODO(), nil)
	_ = db // avoid unused
	assert.ErrorIs(t, err, ErrNilSagaExecution)
}

func TestSagaExecutionRepository_PersistExecution_Update(t *testing.T) {
	db, ctx, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&sagaExecutionEntity{}),
		testdb.WithTenant(testTenantID),
	)
	defer cleanup()

	repo := NewSagaExecutionRepository(db)

	now := time.Now().UTC()
	exec := &domain.SagaExecution{
		ID:             uuid.New(),
		PaymentOrderID: uuid.New(),
		SagaName:       "initiate-payment",
		SagaVersion:    1,
		Status:         domain.SagaExecutionStatusRunning,
		CorrelationID:  "corr-789",
		Input:          map[string]any{"amount": 2000},
		Output:         map[string]any{},
		StartedAt:      now,
	}

	// Insert
	require.NoError(t, repo.PersistExecution(ctx, exec))

	// Update (same ID)
	completedAt := now.Add(time.Second)
	exec.Status = domain.SagaExecutionStatusCompleted
	exec.Output = map[string]any{"result": "settled"}
	exec.CompletedAt = &completedAt
	exec.StepCount = 5
	exec.DurationMs = 1000

	require.NoError(t, repo.PersistExecution(ctx, exec))
}

func TestNewSagaExecutionRepository(t *testing.T) {
	repo := NewSagaExecutionRepository(nil)
	assert.NotNil(t, repo)
}
