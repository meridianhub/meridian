package messaging_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialgatewayeventsv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway_events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// stubPaymentOrderUpdater records calls to UpdatePaymentOrder.
type stubPaymentOrderUpdater struct {
	calls []*pb.UpdatePaymentOrderRequest
	err   error
	resp  *pb.UpdatePaymentOrderResponse
}

func (s *stubPaymentOrderUpdater) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	s.calls = append(s.calls, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &pb.UpdatePaymentOrderResponse{}, nil
}

func TestPaymentEventConsumer_HandlePaymentCapturedEvent(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := &financialgatewayeventsv1.PaymentCapturedEvent{
		EventId:             "evt-cap-1",
		PaymentOrderId:      "po-123",
		ProviderReferenceId: "pi_test_abc",
		ProviderEventId:     "evt-stripe-1",
		Version:             1,
		CapturedAt:          timestamppb.New(time.Now()),
	}

	ctx := context.Background()
	err := consumer.HandlePaymentCapturedEvent(ctx, nil, evt)
	require.NoError(t, err)

	require.Len(t, stub.calls, 1)
	req := stub.calls[0]
	assert.Equal(t, "po-123", req.GetPaymentOrderId())
	assert.Equal(t, "pi_test_abc", req.GetGatewayReferenceId())
	assert.Equal(t, pb.GatewayStatus_GATEWAY_STATUS_SETTLED, req.GetGatewayStatus())
	assert.NotEmpty(t, req.GetIdempotencyKey().GetKey())
}

func TestPaymentEventConsumer_HandlePaymentFailedEvent(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := &financialgatewayeventsv1.PaymentFailedEvent{
		EventId:             "evt-fail-1",
		PaymentOrderId:      "po-456",
		ProviderReferenceId: "pi_test_failed",
		FailureReason:       "card declined",
		FailureCode:         "card_declined",
		ProviderEventId:     "evt-stripe-2",
		Version:             1,
		FailedAt:            timestamppb.New(time.Now()),
	}

	ctx := context.Background()
	err := consumer.HandlePaymentFailedEvent(ctx, nil, evt)
	require.NoError(t, err)

	require.Len(t, stub.calls, 1)
	req := stub.calls[0]
	assert.Equal(t, "po-456", req.GetPaymentOrderId())
	assert.Equal(t, "pi_test_failed", req.GetGatewayReferenceId())
	assert.Equal(t, pb.GatewayStatus_GATEWAY_STATUS_REJECTED, req.GetGatewayStatus())
	assert.Equal(t, "card declined", req.GetGatewayMessage())
	assert.NotEmpty(t, req.GetIdempotencyKey().GetKey())
}

func TestPaymentEventConsumer_HandlePaymentCapturedEvent_UpdateError_Propagates(t *testing.T) {
	stub := &stubPaymentOrderUpdater{err: errors.New("service unavailable")}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := &financialgatewayeventsv1.PaymentCapturedEvent{
		EventId:             "evt-cap-err",
		PaymentOrderId:      "po-err",
		ProviderReferenceId: "pi_test_err",
		ProviderEventId:     "evt-stripe-err",
		Version:             1,
	}

	err := consumer.HandlePaymentCapturedEvent(context.Background(), nil, evt)
	assert.Error(t, err)
}

func TestPaymentEventConsumer_HandlePaymentFailedEvent_UpdateError_Propagates(t *testing.T) {
	stub := &stubPaymentOrderUpdater{err: errors.New("payment order not found")}
	consumer := messaging.NewPaymentEventConsumer(stub)

	evt := &financialgatewayeventsv1.PaymentFailedEvent{
		EventId:             "evt-fail-err",
		PaymentOrderId:      "po-not-found",
		ProviderReferenceId: "pi_test_missing",
		ProviderEventId:     "evt-stripe-missing",
		Version:             1,
	}

	err := consumer.HandlePaymentFailedEvent(context.Background(), nil, evt)
	assert.Error(t, err)
}

func TestPaymentEventConsumer_IdempotencyKey_UsesProviderEventId(t *testing.T) {
	stub := &stubPaymentOrderUpdater{}
	consumer := messaging.NewPaymentEventConsumer(stub)

	providerEventID := "evt_unique_stripe_id"

	evt := &financialgatewayeventsv1.PaymentCapturedEvent{
		EventId:             "domain-evt-1",
		PaymentOrderId:      "po-idem",
		ProviderReferenceId: "pi_idem",
		ProviderEventId:     providerEventID,
		Version:             1,
	}

	require.NoError(t, consumer.HandlePaymentCapturedEvent(context.Background(), nil, evt))

	require.Len(t, stub.calls, 1)
	assert.Contains(t, stub.calls[0].GetIdempotencyKey().GetKey(), providerEventID)
}

// Ensure the stub implements the interface.
var _ messaging.PaymentOrderUpdater = (*stubPaymentOrderUpdater)(nil)

// Ensure commonpb is used (it's used in the implementation, need to avoid import cycle check).
var _ *commonpb.IdempotencyKey
