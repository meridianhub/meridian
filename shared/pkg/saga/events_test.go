package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-specific sentinel errors.
var (
	errStepExecFailed  = errors.New("step execution failed")
	errOutboxWriteFail = errors.New("outbox write failed")
)

// TestStarlarkContext_CorrelationID tests that correlation ID is available in context.
func TestStarlarkContext_CorrelationID(t *testing.T) {
	correlationID := uuid.New()
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   correlationID,
	}

	assert.Equal(t, correlationID, ctx.CorrelationID)
}

// TestSagaProgressEvent tests saga progress event creation.
func TestSagaProgressEvent(t *testing.T) {
	sagaID := uuid.New()
	correlationID := uuid.New()

	event := NewProgressEvent(
		sagaID,
		correlationID,
		2,
		"validate_balance",
		50,
		"Checking account balance",
	)

	assert.Equal(t, sagaID, event.SagaInstanceID)
	assert.Equal(t, correlationID, event.CorrelationID)
	assert.Equal(t, 2, event.StepIndex)
	assert.Equal(t, "validate_balance", event.StepName)
	assert.Equal(t, 50, event.Percentage)
	assert.Equal(t, "Checking account balance", event.Message)
	assert.False(t, event.Timestamp.IsZero())
}

// TestSagaStepCompletedEvent tests saga step completed event creation.
func TestSagaStepCompletedEvent(t *testing.T) {
	sagaID := uuid.New()
	correlationID := uuid.New()
	causationID := uuid.New()

	event := NewStepCompletedEvent(
		sagaID,
		correlationID,
		causationID,
		3,
		"post_entries",
		map[string]any{"posting_ids": []string{"p1", "p2"}},
	)

	assert.Equal(t, sagaID, event.SagaInstanceID)
	assert.Equal(t, correlationID, event.CorrelationID)
	assert.Equal(t, causationID, event.CausationID)
	assert.Equal(t, 3, event.StepIndex)
	assert.Equal(t, "post_entries", event.StepName)
	assert.NotNil(t, event.Result)
	assert.False(t, event.Timestamp.IsZero())
}

// TestSagaStepFailedEvent tests saga step failed event creation.
func TestSagaStepFailedEvent(t *testing.T) {
	sagaID := uuid.New()
	correlationID := uuid.New()
	causationID := uuid.New()

	event := NewStepFailedEvent(
		sagaID,
		correlationID,
		causationID,
		1,
		"create_lien",
		"insufficient funds",
		ErrorCategoryFatal,
	)

	assert.Equal(t, sagaID, event.SagaInstanceID)
	assert.Equal(t, correlationID, event.CorrelationID)
	assert.Equal(t, causationID, event.CausationID)
	assert.Equal(t, 1, event.StepIndex)
	assert.Equal(t, "create_lien", event.StepName)
	assert.Equal(t, "insufficient funds", event.ErrorMessage)
	assert.Equal(t, ErrorCategoryFatal, event.ErrorCategory)
	assert.False(t, event.Timestamp.IsZero())
}

// TestEventPublisher_Interface tests the event publisher interface.
func TestEventPublisher_Interface(t *testing.T) {
	// Mock publisher
	var publishedEvents []Event
	publisher := &MockEventPublisher{
		PublishFunc: func(_ context.Context, event Event) error {
			publishedEvents = append(publishedEvents, event)
			return nil
		},
	}

	ctx := context.Background()
	sagaID := uuid.New()
	correlationID := uuid.New()

	// Publish progress event
	progressEvent := NewProgressEvent(sagaID, correlationID, 0, "step1", 100, "done")
	err := publisher.Publish(ctx, progressEvent)
	require.NoError(t, err)

	assert.Len(t, publishedEvents, 1)
	assert.Equal(t, EventTypeProgress, publishedEvents[0].EventType())
}

// TestEventTypes tests event type identification.
func TestEventTypes(t *testing.T) {
	sagaID := uuid.New()
	correlationID := uuid.New()
	causationID := uuid.New()

	progress := NewProgressEvent(sagaID, correlationID, 0, "step", 50, "msg")
	assert.Equal(t, EventTypeProgress, progress.EventType())

	completed := NewStepCompletedEvent(sagaID, correlationID, causationID, 0, "step", nil)
	assert.Equal(t, EventTypeStepCompleted, completed.EventType())

	failed := NewStepFailedEvent(sagaID, correlationID, causationID, 0, "step", "err", ErrorCategoryFatal)
	assert.Equal(t, EventTypeStepFailed, failed.EventType())
}

// TestOutboxEventPublisher_PublishProgress tests outbox-based progress publishing.
func TestOutboxEventPublisher_PublishProgress(t *testing.T) {
	// Create mock outbox writer
	var writtenEntries []*OutboxEntry
	mockWriter := &MockOutboxWriter{
		WriteFunc: func(_ context.Context, entry *OutboxEntry) error {
			writtenEntries = append(writtenEntries, entry)
			return nil
		},
	}

	publisher := NewOutboxEventPublisher(mockWriter, "saga.events.v1", "saga-service")

	ctx := context.Background()
	sagaID := uuid.New()
	correlationID := uuid.New()

	event := NewProgressEvent(sagaID, correlationID, 2, "step2", 75, "almost done")
	err := publisher.Publish(ctx, event)
	require.NoError(t, err)

	require.Len(t, writtenEntries, 1)
	entry := writtenEntries[0]

	assert.Equal(t, "saga.events.v1", entry.Topic)
	assert.Equal(t, "saga-service", entry.ServiceName)
	assert.Equal(t, correlationID.String(), entry.CorrelationID)
	assert.Equal(t, sagaID.String(), entry.AggregateID)
	assert.Equal(t, "SagaInstance", entry.AggregateType)
	assert.Equal(t, string(EventTypeProgress), entry.EventType)
}

// TestTxContext_WithOutbox tests transactional outbox writing.
func TestTxContext_WithOutbox(t *testing.T) {
	// This tests the interface - actual integration tested in step_execution_integration_test.go
	var savedResults []*SagaStepResult
	var writtenOutboxEntries []*OutboxEntry
	var updatedStepIndexes []int

	mockTx := &MockTxContextWithOutbox{
		SaveStepResultFunc: func(_ context.Context, result *SagaStepResult) error {
			savedResults = append(savedResults, result)
			return nil
		},
		WriteOutboxEntryFunc: func(_ context.Context, entry *OutboxEntry) error {
			writtenOutboxEntries = append(writtenOutboxEntries, entry)
			return nil
		},
		UpdateStepIndexFunc: func(_ context.Context, _ uuid.UUID, stepIndex int) error {
			updatedStepIndexes = append(updatedStepIndexes, stepIndex)
			return nil
		},
		CommitFunc:   func() error { return nil },
		RollbackFunc: func() error { return nil },
	}

	ctx := context.Background()
	sagaID := uuid.New()
	correlationID := uuid.New()

	// Simulate step completion with outbox entry
	result := &SagaStepResult{
		ID:             uuid.New(),
		SagaInstanceID: sagaID,
		StepIndex:      0,
		StepName:       "test_step",
		Status:         StepStatusCompleted,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	err := mockTx.SaveStepResult(ctx, result)
	require.NoError(t, err)

	outboxEntry := &OutboxEntry{
		ID:            uuid.New(),
		EventType:     string(EventTypeStepCompleted),
		AggregateID:   sagaID.String(),
		AggregateType: "SagaInstance",
		CorrelationID: correlationID.String(),
		Topic:         "saga.events.v1",
		ServiceName:   "test-service",
	}

	err = mockTx.WriteOutboxEntry(ctx, outboxEntry)
	require.NoError(t, err)

	err = mockTx.UpdateStepIndex(ctx, sagaID, 1)
	require.NoError(t, err)

	err = mockTx.Commit()
	require.NoError(t, err)

	assert.Len(t, savedResults, 1)
	assert.Len(t, writtenOutboxEntries, 1)
	assert.Equal(t, []int{1}, updatedStepIndexes)
}

// MockEventPublisher is a mock implementation for testing.
type MockEventPublisher struct {
	PublishFunc func(ctx context.Context, event Event) error
}

func (m *MockEventPublisher) Publish(ctx context.Context, event Event) error {
	return m.PublishFunc(ctx, event)
}

// MockOutboxWriter is a mock implementation for testing.
type MockOutboxWriter struct {
	WriteFunc func(ctx context.Context, entry *OutboxEntry) error
}

func (m *MockOutboxWriter) Write(ctx context.Context, entry *OutboxEntry) error {
	return m.WriteFunc(ctx, entry)
}

// MockTxContextWithOutbox is a mock implementation for testing transactional outbox.
type MockTxContextWithOutbox struct {
	SaveStepResultFunc   func(_ context.Context, result *SagaStepResult) error
	WriteOutboxEntryFunc func(ctx context.Context, entry *OutboxEntry) error
	UpdateStepIndexFunc  func(ctx context.Context, instanceID uuid.UUID, stepIndex int) error
	CommitFunc           func() error
	RollbackFunc         func() error
}

func (m *MockTxContextWithOutbox) SaveStepResult(ctx context.Context, result *SagaStepResult) error {
	return m.SaveStepResultFunc(ctx, result)
}

func (m *MockTxContextWithOutbox) WriteOutboxEntry(ctx context.Context, entry *OutboxEntry) error {
	return m.WriteOutboxEntryFunc(ctx, entry)
}

func (m *MockTxContextWithOutbox) UpdateStepIndex(ctx context.Context, instanceID uuid.UUID, stepIndex int) error {
	return m.UpdateStepIndexFunc(ctx, instanceID, stepIndex)
}

func (m *MockTxContextWithOutbox) Commit() error {
	return m.CommitFunc()
}

func (m *MockTxContextWithOutbox) Rollback() error {
	return m.RollbackFunc()
}

// TestExecuteStepWithOutbox_AtomicCommit tests that step result and outbox entry are committed atomically.
func TestExecuteStepWithOutbox_AtomicCommit(t *testing.T) {
	var savedResults []*SagaStepResult
	var writtenOutboxEntries []*OutboxEntry
	var committedTx bool

	mockTx := &MockTxContextWithOutbox{
		SaveStepResultFunc: func(_ context.Context, result *SagaStepResult) error {
			savedResults = append(savedResults, result)
			return nil
		},
		WriteOutboxEntryFunc: func(_ context.Context, entry *OutboxEntry) error {
			writtenOutboxEntries = append(writtenOutboxEntries, entry)
			return nil
		},
		UpdateStepIndexFunc: func(_ context.Context, _ uuid.UUID, _ int) error {
			return nil
		},
		CommitFunc: func() error {
			committedTx = true
			return nil
		},
		RollbackFunc: func() error { return nil },
	}

	executor := NewTransactionalStepExecutor(nil)

	sagaID := uuid.New()
	correlationID := uuid.New()
	instance := &SagaInstance{
		ID:            sagaID,
		CorrelationID: correlationID,
	}

	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"result": "success"}, nil
	}

	result, err := executor.ExecuteStepWithOutbox(
		context.Background(),
		instance,
		0,
		"test_step",
		handler,
		nil,
		mockTx,
	)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, committedTx, "Transaction should be committed")
	assert.Len(t, savedResults, 1, "Step result should be saved")
	assert.Len(t, writtenOutboxEntries, 1, "Outbox entry should be written")

	// Verify outbox entry has correct correlation ID
	entry := writtenOutboxEntries[0]
	assert.Equal(t, correlationID.String(), entry.CorrelationID)
	assert.Equal(t, sagaID.String(), entry.AggregateID)
	assert.Equal(t, string(EventTypeStepCompleted), entry.EventType)
}

// TestExecuteStepWithOutbox_FailedStep tests that failed steps write failure events to outbox.
func TestExecuteStepWithOutbox_FailedStep(t *testing.T) {
	var writtenOutboxEntries []*OutboxEntry

	mockTx := &MockTxContextWithOutbox{
		SaveStepResultFunc: func(_ context.Context, _ *SagaStepResult) error {
			return nil
		},
		WriteOutboxEntryFunc: func(_ context.Context, entry *OutboxEntry) error {
			writtenOutboxEntries = append(writtenOutboxEntries, entry)
			return nil
		},
		UpdateStepIndexFunc: func(_ context.Context, _ uuid.UUID, _ int) error {
			return nil
		},
		CommitFunc: func() error {
			return nil
		},
		RollbackFunc: func() error {
			return nil
		},
	}

	executor := NewTransactionalStepExecutor(nil)

	instance := &SagaInstance{
		ID:            uuid.New(),
		CorrelationID: uuid.New(),
	}

	expectedErr := errStepExecFailed
	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return nil, expectedErr
	}

	result, err := executor.ExecuteStepWithOutbox(
		context.Background(),
		instance,
		0,
		"failing_step",
		handler,
		nil,
		mockTx,
	)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "step execution failed")

	// Even for failed steps, outbox entry should be written
	require.Len(t, writtenOutboxEntries, 1)
	entry := writtenOutboxEntries[0]
	assert.Equal(t, string(EventTypeStepFailed), entry.EventType)
}

// TestExecuteStepWithOutbox_RollbackOnOutboxError tests rollback when outbox write fails.
func TestExecuteStepWithOutbox_RollbackOnOutboxError(t *testing.T) {
	var rolledBack bool

	mockTx := &MockTxContextWithOutbox{
		SaveStepResultFunc: func(_ context.Context, _ *SagaStepResult) error {
			return nil
		},
		WriteOutboxEntryFunc: func(_ context.Context, _ *OutboxEntry) error {
			return errOutboxWriteFail
		},
		UpdateStepIndexFunc: func(_ context.Context, _ uuid.UUID, _ int) error {
			return nil
		},
		CommitFunc: func() error {
			return nil
		},
		RollbackFunc: func() error {
			rolledBack = true
			return nil
		},
	}

	executor := NewTransactionalStepExecutor(nil)

	instance := &SagaInstance{
		ID:            uuid.New(),
		CorrelationID: uuid.New(),
	}

	handler := func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
		return map[string]interface{}{"result": "success"}, nil
	}

	_, err := executor.ExecuteStepWithOutbox(
		context.Background(),
		instance,
		0,
		"test_step",
		handler,
		nil,
		mockTx,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outbox write failed")
	assert.True(t, rolledBack, "Transaction should be rolled back on outbox write failure")
}
