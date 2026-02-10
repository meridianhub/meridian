package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSagaTrigger struct {
	triggerFunc func(ctx context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error)
	calls       []sagaTriggerCall
}

type sagaTriggerCall struct {
	SagaName       string
	InputData      map[string]any
	IdempotencyKey string
}

func (m *mockSagaTrigger) TriggerSaga(ctx context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error) {
	m.calls = append(m.calls, sagaTriggerCall{
		SagaName:       sagaName,
		InputData:      inputData,
		IdempotencyKey: idempotencyKey,
	})
	if m.triggerFunc != nil {
		return m.triggerFunc(ctx, sagaName, inputData, idempotencyKey)
	}
	return NewSagaID(), nil
}

func TestNewPaymentEventConsumer(t *testing.T) {
	t.Run("valid trigger", func(t *testing.T) {
		consumer, err := NewPaymentEventConsumer(&mockSagaTrigger{}, nil)
		assert.NoError(t, err)
		assert.NotNil(t, consumer)
	})

	t.Run("nil trigger", func(t *testing.T) {
		consumer, err := NewPaymentEventConsumer(nil, nil)
		assert.ErrorIs(t, err, ErrNilSagaTrigger)
		assert.Nil(t, consumer)
	})
}

func TestHandlePaymentEvent_PaymentIntentSucceeded(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	event := &PaymentEvent{
		EventID:         "evt-1",
		StripeEventID:   "evt_stripe_123",
		EventType:       "payment_intent.succeeded",
		TenantID:        "meridian-ops",
		PartyID:         "party-abc",
		AmountCents:     10000,
		Currency:        "gbp",
		ChargeID:        "ch_123",
		PaymentIntentID: "pi_456",
		IdempotencyKey:  "stripe:abc123",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	err = consumer.HandlePaymentEvent(context.Background(), data)
	require.NoError(t, err)

	require.Len(t, trigger.calls, 1)
	call := trigger.calls[0]
	assert.Equal(t, "stripe_payment_received", call.SagaName)
	assert.Equal(t, "meridian-ops", call.InputData["tenant_id"])
	assert.Equal(t, "party-abc", call.InputData["party_id"])
	// JSON numbers round-trip as float64 through json.Unmarshal into map[string]any
	assert.InDelta(t, float64(10000), call.InputData["amount_cents"], 0.01)
	assert.Equal(t, "gbp", call.InputData["currency"])
	assert.Equal(t, "ch_123", call.InputData["charge_id"])
	assert.Equal(t, "stripe:abc123", call.IdempotencyKey)
}

func TestHandlePaymentEvent_ChargeRefunded(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	event := &PaymentEvent{
		EventID:         "evt-2",
		StripeEventID:   "evt_stripe_refund",
		EventType:       "charge.refunded",
		TenantID:        "meridian-ops",
		PartyID:         "party-abc",
		AmountCents:     5000,
		Currency:        "gbp",
		ChargeID:        "ch_123",
		PaymentIntentID: "pi_456",
		IdempotencyKey:  "stripe:refund123",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	err = consumer.HandlePaymentEvent(context.Background(), data)
	require.NoError(t, err)

	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "stripe_payment_refunded", trigger.calls[0].SagaName)
}

func TestHandlePaymentEvent_UnknownType(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	event := &PaymentEvent{
		EventID:   "evt-3",
		EventType: "customer.created",
		TenantID:  "meridian-ops",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Unknown types should be silently ignored
	err = consumer.HandlePaymentEvent(context.Background(), data)
	assert.NoError(t, err)
	assert.Empty(t, trigger.calls)
}

func TestHandlePaymentEvent_InvalidJSON(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	err = consumer.HandlePaymentEvent(context.Background(), []byte("not json"))
	assert.ErrorIs(t, err, ErrInvalidPayload)
}

func TestHandlePaymentEvent_SagaTriggerError(t *testing.T) {
	trigger := &mockSagaTrigger{
		triggerFunc: func(_ context.Context, _ string, _ map[string]any, _ string) (string, error) {
			return "", errors.New("saga engine unavailable")
		},
	}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	event := &PaymentEvent{
		EventType:      "payment_intent.succeeded",
		TenantID:       "meridian-ops",
		PartyID:        "party-1",
		AmountCents:    1000,
		ChargeID:       "ch_1",
		IdempotencyKey: "stripe:key1",
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	err = consumer.HandlePaymentEvent(context.Background(), data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "saga engine unavailable")
}

func TestHandlePaymentEvent_IdempotencyKeyPropagated(t *testing.T) {
	trigger := &mockSagaTrigger{}
	consumer, err := NewPaymentEventConsumer(trigger, nil)
	require.NoError(t, err)

	idempotencyKey := "stripe:deterministic_key_abc"
	event := &PaymentEvent{
		EventType:      "payment_intent.succeeded",
		TenantID:       "meridian-ops",
		PartyID:        "party-1",
		AmountCents:    2500,
		ChargeID:       "ch_idem",
		IdempotencyKey: idempotencyKey,
	}

	data, err := json.Marshal(event)
	require.NoError(t, err)

	// Process the same event twice
	err = consumer.HandlePaymentEvent(context.Background(), data)
	require.NoError(t, err)
	err = consumer.HandlePaymentEvent(context.Background(), data)
	require.NoError(t, err)

	// Both calls should have the same idempotency key
	require.Len(t, trigger.calls, 2)
	assert.Equal(t, idempotencyKey, trigger.calls[0].IdempotencyKey)
	assert.Equal(t, idempotencyKey, trigger.calls[1].IdempotencyKey)
}
