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

// ErrNilPaymentOrderUpdater is returned when a nil PaymentOrderUpdater is passed to a constructor.
var ErrNilPaymentOrderUpdater = errors.New("payment order updater is required")

// ErrUnexpectedCapturedMessageType is returned when the payment-captured consumer receives a message that is not *PaymentCapturedEvent.
var ErrUnexpectedCapturedMessageType = errors.New("unexpected message type for payment-captured topic")

// ErrUnexpectedFailedMessageType is returned when the payment-failed consumer receives a message that is not *PaymentFailedEvent.
var ErrUnexpectedFailedMessageType = errors.New("unexpected message type for payment-failed topic")

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
//
// Two separate ProtoConsumers are used so that each topic uses the correct
// proto message factory for deserialization. A single consumer with a shared
// msgFactory would cause PaymentFailedEvent bytes to be deserialized as
// PaymentCapturedEvent, silently corrupting the event.
type PaymentEventConsumer struct {
	capturedConsumer *kafka.ProtoConsumer
	failedConsumer   *kafka.ProtoConsumer
	svc              PaymentOrderUpdater
	logger           *slog.Logger
}

// NewPaymentEventConsumer creates a consumer that handles domain events from financial-gateway.
func NewPaymentEventConsumer(svc PaymentOrderUpdater) *PaymentEventConsumer {
	if svc == nil {
		panic(ErrNilPaymentOrderUpdater.Error())
	}
	return &PaymentEventConsumer{
		svc:    svc,
		logger: slog.Default(),
	}
}

// NewPaymentEventConsumerWithKafka creates a PaymentEventConsumer wired to real Kafka topics.
// Two separate consumers are created — one per topic — so each uses the correct proto
// message factory for deserialization.
//
// Call Start() to begin consuming and Stop()/Close() for graceful shutdown.
func NewPaymentEventConsumerWithKafka(
	kafkaConfig kafka.ConsumerConfig,
	svc PaymentOrderUpdater,
	logger *slog.Logger,
) (*PaymentEventConsumer, error) {
	if svc == nil {
		return nil, ErrNilPaymentOrderUpdater
	}
	if logger == nil {
		logger = slog.Default()
	}

	c := &PaymentEventConsumer{
		svc:    svc,
		logger: logger,
	}

	// Consumer for payment-captured events uses PaymentCapturedEvent as the proto type.
	capturedConfig := kafkaConfig
	capturedConfig.GroupID = kafkaConfig.GroupID + "-captured"
	capturedConfig.ClientID = kafkaConfig.ClientID + "-captured"
	capturedConsumer, err := kafka.NewProtoConsumer(
		capturedConfig,
		func() proto.Message { return &financialgatewayeventsv1.PaymentCapturedEvent{} },
		func(ctx context.Context, key []byte, msg proto.Message) error {
			evt, ok := msg.(*financialgatewayeventsv1.PaymentCapturedEvent)
			if !ok {
				return fmt.Errorf("%w: %T", ErrUnexpectedCapturedMessageType, msg)
			}
			return c.HandlePaymentCapturedEvent(ctx, key, evt)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create payment-captured kafka consumer: %w", err)
	}

	// Consumer for payment-failed events uses PaymentFailedEvent as the proto type.
	failedConfig := kafkaConfig
	failedConfig.GroupID = kafkaConfig.GroupID + "-failed"
	failedConfig.ClientID = kafkaConfig.ClientID + "-failed"
	failedConsumer, err := kafka.NewProtoConsumer(
		failedConfig,
		func() proto.Message { return &financialgatewayeventsv1.PaymentFailedEvent{} },
		func(ctx context.Context, key []byte, msg proto.Message) error {
			evt, ok := msg.(*financialgatewayeventsv1.PaymentFailedEvent)
			if !ok {
				return fmt.Errorf("%w: %T", ErrUnexpectedFailedMessageType, msg)
			}
			return c.HandlePaymentFailedEvent(ctx, key, evt)
		},
	)
	if err != nil {
		_ = capturedConsumer.Close()
		return nil, fmt.Errorf("failed to create payment-failed kafka consumer: %w", err)
	}

	c.capturedConsumer = capturedConsumer
	c.failedConsumer = failedConsumer
	return c, nil
}

// Start subscribes to the financial-gateway payment topics and begins consuming.
// Starts both consumers in goroutines and blocks until both have stopped.
// Returns the first error encountered, or nil if both stop cleanly.
func (c *PaymentEventConsumer) Start(capturedTopic, failedTopic string) error {
	if c.capturedConsumer == nil || c.failedConsumer == nil {
		return ErrConsumerNotConfigured
	}
	c.logger.Info("starting payment event consumers",
		"captured_topic", capturedTopic,
		"failed_topic", failedTopic,
	)

	errs := make(chan error, 2)

	go func() {
		if err := c.capturedConsumer.Subscribe([]string{capturedTopic}); err != nil {
			errs <- fmt.Errorf("payment-captured consumer: %w", err)
		} else {
			errs <- nil
		}
	}()

	go func() {
		if err := c.failedConsumer.Subscribe([]string{failedTopic}); err != nil {
			errs <- fmt.Errorf("payment-failed consumer: %w", err)
		} else {
			errs <- nil
		}
	}()

	// Wait for both consumers to stop.
	var firstErr error
	for range 2 {
		if err := <-errs; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Stop gracefully stops both Kafka consumers.
func (c *PaymentEventConsumer) Stop() {
	if c.capturedConsumer != nil {
		c.capturedConsumer.Stop()
	}
	if c.failedConsumer != nil {
		c.failedConsumer.Stop()
	}
}

// Close closes both consumers and releases resources.
func (c *PaymentEventConsumer) Close() error {
	var capturedErr, failedErr error
	if c.capturedConsumer != nil {
		capturedErr = c.capturedConsumer.Close()
	}
	if c.failedConsumer != nil {
		failedErr = c.failedConsumer.Close()
	}
	if capturedErr != nil {
		return capturedErr
	}
	return failedErr
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
