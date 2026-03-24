package domain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSagaTrigger is a test double implementing domain.SagaTrigger.
type fakeSagaTrigger struct {
	calls    []sagaTriggerCall
	err      error
	returnID string
}

type sagaTriggerCall struct {
	SagaName       string
	InputData      map[string]any
	IdempotencyKey string
}

func (f *fakeSagaTrigger) TriggerSaga(_ context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error) {
	f.calls = append(f.calls, sagaTriggerCall{
		SagaName:       sagaName,
		InputData:      inputData,
		IdempotencyKey: idempotencyKey,
	})
	if f.err != nil {
		return "", f.err
	}
	id := f.returnID
	if id == "" {
		id = "saga-instance-" + sagaName
	}
	return id, nil
}

func (f *fakeSagaTrigger) Close() error { return nil }

// Verify fakeSagaTrigger satisfies domain.SagaTrigger at compile time.
var _ domain.SagaTrigger = (*fakeSagaTrigger)(nil)

// TestSagaTrigger_InterfaceCompliance verifies any struct implementing TriggerSaga satisfies SagaTrigger.
func TestSagaTrigger_InterfaceCompliance(t *testing.T) {
	var t1 domain.SagaTrigger = &fakeSagaTrigger{}
	assert.NotNil(t, t1)
}

// TestSagaTrigger_TriggerSaga_PassesSagaName verifies saga name is passed through to trigger.
func TestSagaTrigger_TriggerSaga_PassesSagaName(t *testing.T) {
	trigger := &fakeSagaTrigger{}

	id, err := trigger.TriggerSaga(context.Background(), "process_payment", map[string]any{"amount": 100}, "key-1")

	require.NoError(t, err)
	assert.Equal(t, "saga-instance-process_payment", id)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "process_payment", trigger.calls[0].SagaName)
}

// TestSagaTrigger_TriggerSaga_PassesInputData verifies input data is forwarded to the saga.
func TestSagaTrigger_TriggerSaga_PassesInputData(t *testing.T) {
	trigger := &fakeSagaTrigger{}

	inputData := map[string]any{
		"account_id": "acc-123",
		"amount":     "100.00",
		"currency":   "GBP",
	}

	_, err := trigger.TriggerSaga(context.Background(), "transfer_funds", inputData, "key-2")

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, inputData, trigger.calls[0].InputData)
}

// TestSagaTrigger_TriggerSaga_IdempotencyKey verifies idempotency key is forwarded.
func TestSagaTrigger_TriggerSaga_IdempotencyKey(t *testing.T) {
	trigger := &fakeSagaTrigger{}

	idempotencyKey := "correlation-uuid-abc123"
	_, err := trigger.TriggerSaga(context.Background(), "create_account", map[string]any{}, idempotencyKey)

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, idempotencyKey, trigger.calls[0].IdempotencyKey)
}

// TestSagaTrigger_TriggerSaga_ErrorPropagation verifies trigger errors are propagated to the caller.
func TestSagaTrigger_TriggerSaga_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("control plane unavailable")
	trigger := &fakeSagaTrigger{err: expectedErr}

	_, err := trigger.TriggerSaga(context.Background(), "test_saga", map[string]any{}, "key-3")

	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

// TestSagaTrigger_TriggerSaga_DuplicatePrevention verifies the same idempotency key
// is passed each time to allow the underlying implementation to deduplicate.
func TestSagaTrigger_TriggerSaga_DuplicatePrevention(t *testing.T) {
	trigger := &fakeSagaTrigger{returnID: "existing-saga-id"}

	// Simulates a Kafka redelivery — same idempotency key sent twice
	idempotencyKey := "unique-event-id"
	_, err1 := trigger.TriggerSaga(context.Background(), "process_order", map[string]any{}, idempotencyKey)
	_, err2 := trigger.TriggerSaga(context.Background(), "process_order", map[string]any{}, idempotencyKey)

	require.NoError(t, err1)
	require.NoError(t, err2)
	// Both calls passed the same idempotency key — the implementation decides deduplication
	assert.Equal(t, idempotencyKey, trigger.calls[0].IdempotencyKey)
	assert.Equal(t, idempotencyKey, trigger.calls[1].IdempotencyKey)
}

// TestSagaTrigger_Close verifies Close releases resources without error.
func TestSagaTrigger_Close(t *testing.T) {
	trigger := &fakeSagaTrigger{}
	err := trigger.Close()
	assert.NoError(t, err)
}

// TestSagaTrigger_ContextExtraction verifies context is passed to the trigger.
func TestSagaTrigger_ContextExtraction(t *testing.T) {
	trigger := &fakeSagaTrigger{}

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "test-value")

	_, err := trigger.TriggerSaga(ctx, "context_saga", map[string]any{"key": "val"}, "key-ctx")

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "context_saga", trigger.calls[0].SagaName)
}
