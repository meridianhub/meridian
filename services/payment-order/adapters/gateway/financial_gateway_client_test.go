package gateway_test

import (
	"context"
	"log/slog"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	"github.com/meridianhub/meridian/services/financial-gateway/client"
	"github.com/meridianhub/meridian/services/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/services/payment-order/domain"
)

// fakeFinancialGatewayServer implements FinancialGatewayServiceServer for testing.
type fakeFinancialGatewayServer struct {
	financialgatewayv1.UnimplementedFinancialGatewayServiceServer
	dispatchPaymentFn func(*financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error)
}

func (f *fakeFinancialGatewayServer) DispatchPayment(_ context.Context, req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
	if f.dispatchPaymentFn != nil {
		return f.dispatchPaymentFn(req)
	}
	return nil, status.Error(codes.Unimplemented, "not configured")
}

// setupTestGRPC starts an in-process gRPC server with the provided fake and returns a connected client.
func setupTestGRPC(t *testing.T, fake *fakeFinancialGatewayServer) *client.Client {
	t.Helper()

	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	financialgatewayv1.RegisterFinancialGatewayServiceServer(srv, fake)

	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.GracefulStop)

	fgClient, cleanup, err := client.New(client.Config{
		Target: lis.Addr().String(),
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	return fgClient
}

func makePaymentRequest(t *testing.T) gateway.PaymentRequest {
	t.Helper()
	amount := domain.MustNewMoney("GBP", 10000)
	return gateway.PaymentRequest{
		PaymentOrderID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DebtorAccountID:   "cus_test123",
		CreditorReference: "pm_testABC",
		Amount:            amount,
		IdempotencyKey:    "idem-key-001",
	}
}

func TestFinancialGatewayClient_SendPayment_Accepted(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			assert.Equal(t, "11111111-1111-1111-1111-111111111111", req.GetPaymentOrderId())
			assert.Equal(t, financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE, req.GetRail())
			assert.Equal(t, int64(10000), req.GetAmountUnits())
			assert.Equal(t, "GBP", req.GetInstrumentCode())
			assert.Equal(t, "cus_test123", req.GetDebtorAccountId())
			assert.Equal(t, "pm_testABC", req.GetReference())
			assert.Equal(t, "idem-key-001", req.GetIdempotencyKey().GetKey())

			return &financialgatewayv1.DispatchPaymentResponse{
				DispatchId:        uuid.New().String(),
				PaymentOrderId:    req.GetPaymentOrderId(),
				Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
				Status:            financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DELIVERED,
				ProviderReference: "pi_stripe_abc",
				CreatedAt:         timestamppb.Now(),
			}, nil
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	resp, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
	assert.Equal(t, "pi_stripe_abc", resp.GatewayReferenceID)
}

func TestFinancialGatewayClient_SendPayment_Acknowledged(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return &financialgatewayv1.DispatchPaymentResponse{
				DispatchId:        uuid.New().String(),
				PaymentOrderId:    req.GetPaymentOrderId(),
				Rail:              financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
				Status:            financialgatewayv1.DispatchStatus_DISPATCH_STATUS_ACKNOWLEDGED,
				ProviderReference: "pi_stripe_xyz",
				CreatedAt:         timestamppb.Now(),
			}, nil
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	resp, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusAccepted, resp.Status)
}

func TestFinancialGatewayClient_SendPayment_Pending(t *testing.T) {
	for _, dispatchStatus := range []financialgatewayv1.DispatchStatus{
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING,
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING,
		financialgatewayv1.DispatchStatus_DISPATCH_STATUS_RETRYING,
	} {
		t.Run(dispatchStatus.String(), func(t *testing.T) {
			fake := &fakeFinancialGatewayServer{
				dispatchPaymentFn: func(req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
					return &financialgatewayv1.DispatchPaymentResponse{
						DispatchId:     uuid.New().String(),
						PaymentOrderId: req.GetPaymentOrderId(),
						Rail:           financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
						Status:         dispatchStatus,
						CreatedAt:      timestamppb.Now(),
					}, nil
				},
			}

			fgClient := setupTestGRPC(t, fake)
			c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

			resp, err := c.SendPayment(context.Background(), makePaymentRequest(t))
			require.NoError(t, err)
			assert.Equal(t, gateway.StatusPending, resp.Status)
		})
	}
}

func TestFinancialGatewayClient_SendPayment_Failed(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return &financialgatewayv1.DispatchPaymentResponse{
				DispatchId:     uuid.New().String(),
				PaymentOrderId: req.GetPaymentOrderId(),
				Rail:           financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
				Status:         financialgatewayv1.DispatchStatus_DISPATCH_STATUS_FAILED,
				CreatedAt:      timestamppb.Now(),
			}, nil
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	resp, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.NoError(t, err)
	assert.Equal(t, gateway.StatusRejected, resp.Status)
}

func TestFinancialGatewayClient_SendPayment_GRPCUnavailable(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Unavailable, "stripe temporarily unavailable")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayUnavailable)
}

func TestFinancialGatewayClient_SendPayment_GRPCInvalidArgument(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "invalid currency code")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayInvalidArgument)
}

func TestFinancialGatewayClient_SendPayment_GRPCFailedPrecondition(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "stripe not configured for tenant")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayPreconditionFailed)
}

func TestFinancialGatewayClient_SendPayment_GRPCDeadlineExceeded(t *testing.T) {
	fake := &fakeFinancialGatewayServer{
		dispatchPaymentFn: func(_ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "deadline exceeded")
		},
	}

	fgClient := setupTestGRPC(t, fake)
	c := gateway.NewFinancialGatewayClient(fgClient, slog.Default())

	_, err := c.SendPayment(context.Background(), makePaymentRequest(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, gateway.ErrFinancialGatewayTimeout)
}

func TestFinancialGatewayClient_ImplementsInterface(t *testing.T) {
	fgClient := setupTestGRPC(t, &fakeFinancialGatewayServer{})
	var _ gateway.PaymentGateway = gateway.NewFinancialGatewayClient(fgClient, nil)
}
