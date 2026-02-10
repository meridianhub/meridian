package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// SagaTrigger abstracts the saga orchestration layer.
// It accepts a saga name and input data, returning the saga instance ID.
type SagaTrigger interface {
	// TriggerSaga starts a new saga instance with the given name and input data.
	// The idempotency key ensures duplicate triggers are handled correctly.
	TriggerSaga(ctx context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error)
}

// PaymentEventConsumer processes payment events from Kafka and triggers sagas.
type PaymentEventConsumer struct {
	sagaTrigger SagaTrigger
	logger      *slog.Logger
}

// Consumer errors.
var (
	ErrNilSagaTrigger   = errors.New("saga trigger cannot be nil")
	ErrUnknownEventType = errors.New("unknown payment event type")
	ErrInvalidPayload   = errors.New("invalid payment event payload")
)

// NewPaymentEventConsumer creates a new consumer that routes payment events to sagas.
func NewPaymentEventConsumer(trigger SagaTrigger, logger *slog.Logger) (*PaymentEventConsumer, error) {
	if trigger == nil {
		return nil, ErrNilSagaTrigger
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentEventConsumer{
		sagaTrigger: trigger,
		logger:      logger,
	}, nil
}

// HandlePaymentEvent processes a single payment event from Kafka.
// This is the Kafka message handler function compatible with the platform consumer.
func (c *PaymentEventConsumer) HandlePaymentEvent(ctx context.Context, data []byte) error {
	var event PaymentEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}

	c.logger.Info("processing payment event",
		"event_type", event.EventType,
		"stripe_event_id", event.StripeEventID,
		"tenant_id", event.TenantID,
		"party_id", event.PartyID,
		"amount_cents", event.AmountCents,
	)

	switch event.EventType {
	case EventTypePaymentIntentSucceeded:
		return c.triggerPaymentReceivedSaga(ctx, &event)
	case EventTypeChargeRefunded:
		return c.triggerRefundSaga(ctx, &event)
	default:
		c.logger.Debug("ignoring unhandled event type", "event_type", event.EventType)
		return nil
	}
}

// triggerPaymentReceivedSaga starts the stripe_payment_received saga.
func (c *PaymentEventConsumer) triggerPaymentReceivedSaga(ctx context.Context, event *PaymentEvent) error {
	inputData := map[string]any{
		"tenant_id":         event.TenantID,
		"party_id":          event.PartyID,
		"amount_cents":      event.AmountCents,
		"currency":          event.Currency,
		"charge_id":         event.ChargeID,
		"payment_intent_id": event.PaymentIntentID,
		"stripe_event_id":   event.StripeEventID,
	}

	sagaID, err := c.sagaTrigger.TriggerSaga(ctx, "stripe_payment_received", inputData, event.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("failed to trigger stripe_payment_received saga: %w", err)
	}

	c.logger.Info("stripe_payment_received saga triggered",
		"saga_id", sagaID,
		"stripe_event_id", event.StripeEventID,
		"charge_id", event.ChargeID,
		"tenant_id", event.TenantID,
	)

	return nil
}

// triggerRefundSaga starts the stripe_payment_refunded saga.
// Note: The refund saga reverses the original double-entry posting.
func (c *PaymentEventConsumer) triggerRefundSaga(ctx context.Context, event *PaymentEvent) error {
	inputData := map[string]any{
		"tenant_id":         event.TenantID,
		"party_id":          event.PartyID,
		"amount_cents":      event.AmountCents,
		"currency":          event.Currency,
		"charge_id":         event.ChargeID,
		"payment_intent_id": event.PaymentIntentID,
		"stripe_event_id":   event.StripeEventID,
	}

	sagaID, err := c.sagaTrigger.TriggerSaga(ctx, "stripe_payment_refunded", inputData, event.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("failed to trigger stripe_payment_refunded saga: %w", err)
	}

	c.logger.Info("stripe_payment_refunded saga triggered",
		"saga_id", sagaID,
		"stripe_event_id", event.StripeEventID,
		"charge_id", event.ChargeID,
	)

	return nil
}

// NewSagaID generates a new UUID for a saga instance.
func NewSagaID() string {
	return uuid.New().String()
}
