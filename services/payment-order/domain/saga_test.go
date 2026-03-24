package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSagaExecutionLogger implements domain.SagaExecutionLogger for testing.
type mockSagaExecutionLogger struct {
	calls     []*domain.SagaExecution
	returnErr error
}

func (m *mockSagaExecutionLogger) PersistExecution(_ context.Context, execution *domain.SagaExecution) error {
	m.calls = append(m.calls, execution)
	return m.returnErr
}

// TestSagaExecutionStatus_Constants verifies all status constants are distinct.
func TestSagaExecutionStatus_Constants(t *testing.T) {
	t.Parallel()

	statuses := []domain.SagaExecutionStatus{
		domain.SagaExecutionStatusRunning,
		domain.SagaExecutionStatusCompleted,
		domain.SagaExecutionStatusFailed,
		domain.SagaExecutionStatusCompensated,
	}

	seen := make(map[domain.SagaExecutionStatus]bool)
	for _, s := range statuses {
		assert.False(t, seen[s], "duplicate status: %s", s)
		seen[s] = true
		assert.NotEmpty(t, string(s))
	}
}

// TestSagaExecution_ZeroValue verifies the zero value is usable.
func TestSagaExecution_ZeroValue(t *testing.T) {
	t.Parallel()

	var exec domain.SagaExecution
	assert.Equal(t, uuid.UUID{}, exec.ID)
	assert.Equal(t, uuid.UUID{}, exec.PaymentOrderID)
	assert.Empty(t, exec.SagaName)
	assert.Equal(t, 0, exec.SagaVersion)
	assert.Equal(t, domain.SagaExecutionStatus(""), exec.Status)
	assert.Nil(t, exec.CompletedAt)
}

// TestSagaExecution_FullConstruction verifies all fields can be set and retrieved.
func TestSagaExecution_FullConstruction(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	poID := uuid.New()
	now := time.Now()
	completedAt := now.Add(5 * time.Second)

	exec := domain.SagaExecution{
		ID:             id,
		PaymentOrderID: poID,
		SagaName:       "payment_execution",
		SagaVersion:    3,
		Status:         domain.SagaExecutionStatusCompleted,
		CorrelationID:  uuid.New().String(),
		Input:          map[string]any{"amount_cents": int64(1000), "currency": "GBP"},
		Output:         map[string]any{"lien_id": "lien-123", "gateway_reference_id": "GW-456"},
		ErrorMessage:   "",
		StepCount:      4,
		DurationMs:     250,
		StartedAt:      now,
		CompletedAt:    &completedAt,
	}

	assert.Equal(t, id, exec.ID)
	assert.Equal(t, poID, exec.PaymentOrderID)
	assert.Equal(t, "payment_execution", exec.SagaName)
	assert.Equal(t, 3, exec.SagaVersion)
	assert.Equal(t, domain.SagaExecutionStatusCompleted, exec.Status)
	assert.NotEmpty(t, exec.CorrelationID)
	assert.Equal(t, int64(1000), exec.Input["amount_cents"])
	assert.Equal(t, "lien-123", exec.Output["lien_id"])
	assert.Equal(t, 4, exec.StepCount)
	assert.Equal(t, int64(250), exec.DurationMs)
	assert.Equal(t, &completedAt, exec.CompletedAt)
}

// TestSagaExecution_FailedStatusWithError verifies failed execution fields.
func TestSagaExecution_FailedStatusWithError(t *testing.T) {
	t.Parallel()

	exec := domain.SagaExecution{
		ID:           uuid.New(),
		Status:       domain.SagaExecutionStatusFailed,
		ErrorMessage: "gateway timeout: context deadline exceeded",
		StepCount:    2,
	}

	assert.Equal(t, domain.SagaExecutionStatusFailed, exec.Status)
	assert.NotEmpty(t, exec.ErrorMessage)
	assert.Nil(t, exec.CompletedAt)
}

// TestSagaExecution_CompensatedStatus verifies compensated status is a distinct state.
func TestSagaExecution_CompensatedStatus(t *testing.T) {
	t.Parallel()

	exec := domain.SagaExecution{
		Status: domain.SagaExecutionStatusCompensated,
	}

	assert.Equal(t, domain.SagaExecutionStatusCompensated, exec.Status)
	assert.NotEqual(t, domain.SagaExecutionStatusFailed, exec.Status)
}

// TestSagaExecutionLogger_PersistExecution verifies the mock logger records calls.
func TestSagaExecutionLogger_PersistExecution(t *testing.T) {
	t.Parallel()

	logger := &mockSagaExecutionLogger{}
	exec := &domain.SagaExecution{
		ID:       uuid.New(),
		SagaName: "payment_execution",
		Status:   domain.SagaExecutionStatusRunning,
	}

	err := logger.PersistExecution(context.Background(), exec)
	require.NoError(t, err)
	assert.Len(t, logger.calls, 1)
	assert.Equal(t, exec.ID, logger.calls[0].ID)
}

// TestSagaExecutionLogger_ErrorPropagation verifies that logger errors are returned to callers.
func TestSagaExecutionLogger_ErrorPropagation(t *testing.T) {
	t.Parallel()

	expectedErr := assert.AnError
	logger := &mockSagaExecutionLogger{returnErr: expectedErr}

	err := logger.PersistExecution(context.Background(), &domain.SagaExecution{})
	assert.ErrorIs(t, err, expectedErr)
}
