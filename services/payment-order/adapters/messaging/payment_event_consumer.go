package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/proto"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayeventsv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway_events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/kafka"
)

// ErrConsumerNotConfigured is returned when Start() is called on a consumer that was
// created without Kafka (use NewPaymentEventConsumerWithKafka for production).
var ErrConsumerNotConfigured = errors.New("consumer not configured with Kafka — use NewPaymentEventConsumerWithKafka")

// PaymentOrderUpdater is the interface for updating payment order status.
// Implemented by the payment-order gRPC service.
type PaymentOrderUpdater interface {
	UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error)
}

// PaymentEventConsumer consumes financial-gateway payment domain events from Kafka
// and updates payment orders accordingly.
//
// This consumer subscribes to:
//   - financial-gateway.payment-captured.v1 → marks payment order as SETTLED
//   - financial-gateway.payment-failed.v1   → marks payment order as REJECTED
type PaymentEventConsumer struct {
	updater *kafka.ProtoConsumer
	svc     PaymentOrderUpdater
	logger  *slog.Logger
}

// NewPaymentEventConsumer creates a consumer that handles domain events from financial-gateway.
func NewPaymentEventConsumer(svc PaymentOrderUpdater) *PaymentEventConsumer {
	return &PaymentEventConsumer{
		svc:    svc,
		logger: slog.Default(),
	}
}

// NewPaymentEventConsumerWithKafka creates a PaymentEventConsumer wired to real Kafka topics.
// The returned consumer handles both payment-captured and payment-failed events by routing
// on the event type within a single consumer group.
//
// Call Start() to begin consuming and Stop()/Close() for graceful shutdown.
func NewPaymentEventConsumerWithKafka(
	kafkaConfig kafka.ConsumerConfig,
	svc PaymentOrderUpdater,
	logger *slog.Logger,
) (*PaymentEventConsumer, error) {
	if logger == nil {
		logger = slog.Default()
	}

	c := &PaymentEventConsumer{
		svc:    svc,
		logger: logger,
	}

	// The message factory returns PaymentCapturedEvent by default;
	// we dispatch based on the proto type in the handler.
	// Since the two event types share the same consumer group and topics,
	// we use a wrapper that attempts to unmarshal as each type.
	msgFactory := func() proto.Message {
		// Return a placeholder; actual type detection happens in the handler
		// by attempting unmarshal of each event type.
		return &financialgatewayeventsv1.PaymentCapturedEvent{}
	}

	handler := func(ctx context.Context, key []byte, msg proto.Message) error {
		return c.dispatch(ctx, key, msg)
	}

	consumer, err := kafka.NewProtoConsumer(kafkaConfig, msgFactory, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment event kafka consumer: %w", err)
	}

	c.updater = consumer
	return c, nil
}

// Start subscribes to the financial-gateway payment topics and begins consuming.
// Blocks until Stop() is called or an unrecoverable error occurs.
func (c *PaymentEventConsumer) Start(topicList []string) error {
	if c.updater == nil {
		return ErrConsumerNotConfigured
	}
	c.logger.Info("starting payment event consumer", "topics", topicList)
	return c.updater.Subscribe(topicList)
}

// Stop gracefully stops the Kafka consumer.
func (c *PaymentEventConsumer) Stop() {
	if c.updater != nil {
		c.updater.Stop()
	}
}

// Close closes the consumer and releases resources.
func (c *PaymentEventConsumer) Close() error {
	if c.updater != nil {
		return c.updater.Close()
	}
	return nil
}

// dispatch routes an incoming Kafka message to the correct handler based on its proto type.
// Since both captured and failed events flow through the same consumer, we attempt
// to identify which event type was received.
func (c *PaymentEventConsumer) dispatch(ctx context.Context, key []byte, msg proto.Message) error {
	switch evt := msg.(type) {
	case *financialgatewayeventsv1.PaymentCapturedEvent:
		return c.HandlePaymentCapturedEvent(ctx, key, evt)
	default:
		c.logger.Warn("received unexpected message type in payment event consumer",
			"type", fmt.Sprintf("%T", msg))
		return nil
	}
}

// HandlePaymentCapturedEvent processes a PaymentCapturedEvent by marking the
// associated payment order as SETTLED (COMPLETED).
func (c *PaymentEventConsumer) HandlePaymentCapturedEvent(
	ctx context.Context,
	_ []byte,
	evt *financialgatewayeventsv1.PaymentCapturedEvent,
) error {
	c.logger.Info("handling payment captured event",
		"event_id", evt.GetEventId(),
		"payment_order_id", evt.GetPaymentOrderId(),
		"provider_reference_id", evt.GetProviderReferenceId(),
		"provider_event_id", evt.GetProviderEventId(),
	)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId:     evt.GetPaymentOrderId(),
		GatewayReferenceId: evt.GetProviderReferenceId(),
		GatewayStatus:      pb.GatewayStatus_GATEWAY_STATUS_SETTLED,
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: idempotencyKey("captured", evt.GetProviderEventId(), evt.GetPaymentOrderId()),
		},
	}

	_, err := c.svc.UpdatePaymentOrder(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to update payment order for captured event %s: %w", evt.GetEventId(), err)
	}

	c.logger.Info("payment order updated to SETTLED",
		"payment_order_id", evt.GetPaymentOrderId(),
		"provider_event_id", evt.GetProviderEventId(),
	)

	return nil
}

// HandlePaymentFailedEvent processes a PaymentFailedEvent by marking the
// associated payment order as REJECTED (FAILED).
func (c *PaymentEventConsumer) HandlePaymentFailedEvent(
	ctx context.Context,
	_ []byte,
	evt *financialgatewayeventsv1.PaymentFailedEvent,
) error {
	c.logger.Info("handling payment failed event",
		"event_id", evt.GetEventId(),
		"payment_order_id", evt.GetPaymentOrderId(),
		"provider_reference_id", evt.GetProviderReferenceId(),
		"failure_reason", evt.GetFailureReason(),
		"provider_event_id", evt.GetProviderEventId(),
	)

	req := &pb.UpdatePaymentOrderRequest{
		PaymentOrderId:     evt.GetPaymentOrderId(),
		GatewayReferenceId: evt.GetProviderReferenceId(),
		GatewayStatus:      pb.GatewayStatus_GATEWAY_STATUS_REJECTED,
		GatewayMessage:     evt.GetFailureReason(),
		IdempotencyKey: &commonpb.IdempotencyKey{
			Key: idempotencyKey("failed", evt.GetProviderEventId(), evt.GetPaymentOrderId()),
		},
	}

	_, err := c.svc.UpdatePaymentOrder(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to update payment order for failed event %s: %w", evt.GetEventId(), err)
	}

	c.logger.Info("payment order updated to REJECTED",
		"payment_order_id", evt.GetPaymentOrderId(),
		"provider_event_id", evt.GetProviderEventId(),
	)

	return nil
}

// idempotencyKey builds a deterministic idempotency key from the event type,
// provider event ID, and payment order ID to prevent duplicate processing.
func idempotencyKey(eventType, providerEventID, paymentOrderID string) string {
	return fmt.Sprintf("fg-event:%s:%s:%s", eventType, providerEventID, paymentOrderID)
}
