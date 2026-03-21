package messaging_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	financialgatewayeventsv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway_events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
)

// =============================================================================
// Constructor edge cases
// =============================================================================

func TestNewPaymentEventConsumer_NilSvc_Panics(t *testing.T) {
	assert.PanicsWithValue(t, messaging.ErrNilPaymentOrderUpdater.Error(), func() {
		messaging.NewPaymentEventConsumer(nil)
	})
}

func TestNewPaymentEventConsumer_ValidSvc(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)
	require.NotNil(t, consumer)
}

// =============================================================================
// Start — not configured (no Kafka)
// =============================================================================

func TestPaymentEventConsumer_Start_NotConfigured(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	// Start should return ErrConsumerNotConfigured because no Kafka was wired
	err := consumer.Start("captured-topic", "failed-topic")
	assert.ErrorIs(t, err, messaging.ErrConsumerNotConfigured)
}

// =============================================================================
// Stop — no panic on nil consumers
// =============================================================================

func TestPaymentEventConsumer_Stop_NilConsumers(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	// Should not panic when internal consumers are nil
	consumer.Stop()
}

// =============================================================================
// Close — no panic on nil consumers
// =============================================================================

func TestPaymentEventConsumer_Close_NilConsumers(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	// Should not panic and return nil when internal consumers are nil
	err := consumer.Close()
	assert.NoError(t, err)
}

// =============================================================================
// IdempotencyKey deterministic format
// =============================================================================

func TestPaymentEventConsumer_IdempotencyKey_Failed_UsesProviderEventId(t *testing.T) {
	stub := &stubPaymentOrderUpdater{
		resp: &pb.UpdatePaymentOrderResponse{},
	}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := newFailedEvent("evt-1", "po-1", "pi_ref", "declined", "evt-stripe-1")

	require.NoError(t, consumer.HandlePaymentFailedEvent(t.Context(), nil, evt))
	require.Len(t, stub.calls, 1)

	key := stub.calls[0].GetIdempotencyKey().GetKey()
	assert.Contains(t, key, "failed")
	assert.Contains(t, key, "evt-stripe-1")
	assert.Contains(t, key, "po-1")
}

func TestPaymentEventConsumer_IdempotencyKey_Captured_Format(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := newCapturedEvent("evt-2", "po-2", "pi_ref_2", "evt-stripe-2")

	require.NoError(t, consumer.HandlePaymentCapturedEvent(t.Context(), nil, evt))
	require.Len(t, stub.calls, 1)

	key := stub.calls[0].GetIdempotencyKey().GetKey()
	assert.Contains(t, key, "captured")
	assert.Contains(t, key, "evt-stripe-2")
	assert.Contains(t, key, "po-2")
}

// =============================================================================
// Helpers
// =============================================================================

func newCapturedEvent(eventID, poID, providerRef, providerEventID string) *financialgatewayeventsv1.PaymentCapturedEvent {
	return &financialgatewayeventsv1.PaymentCapturedEvent{
		EventId:             eventID,
		PaymentOrderId:      poID,
		ProviderReferenceId: providerRef,
		ProviderEventId:     providerEventID,
		Version:             1,
	}
}

func newFailedEvent(eventID, poID, providerRef, reason, providerEventID string) *financialgatewayeventsv1.PaymentFailedEvent {
	return &financialgatewayeventsv1.PaymentFailedEvent{
		EventId:             eventID,
		PaymentOrderId:      poID,
		ProviderReferenceId: providerRef,
		FailureReason:       reason,
		ProviderEventId:     providerEventID,
		Version:             1,
	}
}
