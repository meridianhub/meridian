package gateway_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
)

func TestFinancialGatewayClient_SendPayment_GRPCAlreadyExists(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "duplicate idempotency key")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayDuplicateDispatch)
}

func TestFinancialGatewayClient_SendPayment_GRPCCanceled(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Canceled, "request canceled")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayTimeout)
}

func TestFinancialGatewayClient_SendPayment_GRPCUnimplemented(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Unimplemented, "rail not supported")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayRailUnsupported)
}

func TestFinancialGatewayClient_SendPayment_GRPCUnexpectedCode(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Internal, "unexpected internal error")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayUnexpected)
}

func TestFinancialGatewayClient_SendPayment_UnspecifiedStatus(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return &financialgatewayv1.DispatchPaymentResponse{
				DispatchId:     "dispatch-1",
				PaymentOrderId: req.GetPaymentOrderId(),
				Status:         financialgatewayv1.DispatchStatus_DISPATCH_STATUS_UNSPECIFIED,
			}, nil
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	resp, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusPending, resp.Status)
}

func TestFinancialGatewayClient_NewWithNilLogger(t *testing.T) {
	fgClient := setupTestGRPC(t, &fakeFinancialGatewayServer{})
	c := gateway.NewFinancialGatewayClient(fgClient, nil)
	assert.NotNil(t, c)
}
