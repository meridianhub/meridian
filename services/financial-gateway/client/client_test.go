package client

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
)

const bufSize = 1024 * 1024

// fakeServer implements the gRPC service for testing.
type fakeServer struct {
	financialgatewayv1.UnimplementedFinancialGatewayServiceServer

	dispatchFn func(ctx context.Context, req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error)
	cancelFn   func(ctx context.Context, req *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error)
	refundFn   func(ctx context.Context, req *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error)
	healthFn   func(ctx context.Context, req *financialgatewayv1.GetProviderHealthRequest) (*financialgatewayv1.GetProviderHealthResponse, error)
}

func (s *fakeServer) DispatchPayment(ctx context.Context, req *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
	if s.dispatchFn != nil {
		return s.dispatchFn(ctx, req)
	}
	return &financialgatewayv1.DispatchPaymentResponse{
		DispatchId:     "dispatch-1",
		PaymentOrderId: req.GetPaymentOrderId(),
		Status:         financialgatewayv1.DispatchStatus_DISPATCH_STATUS_DISPATCHING,
	}, nil
}

func (s *fakeServer) CancelPayment(ctx context.Context, req *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
	if s.cancelFn != nil {
		return s.cancelFn(ctx, req)
	}
	return &financialgatewayv1.CancelPaymentResponse{
		PaymentOrderId: req.GetPaymentOrderId(),
		Status:         "CANCELLED",
	}, nil
}

func (s *fakeServer) DispatchRefund(ctx context.Context, req *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error) {
	if s.refundFn != nil {
		return s.refundFn(ctx, req)
	}
	return &financialgatewayv1.DispatchRefundResponse{
		DispatchId: "refund-1",
		Status:     financialgatewayv1.DispatchStatus_DISPATCH_STATUS_PENDING,
	}, nil
}

func (s *fakeServer) GetProviderHealth(ctx context.Context, req *financialgatewayv1.GetProviderHealthRequest) (*financialgatewayv1.GetProviderHealthResponse, error) {
	if s.healthFn != nil {
		return s.healthFn(ctx, req)
	}
	return &financialgatewayv1.GetProviderHealthResponse{
		Rail:   req.GetRail(),
		Health: financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY,
	}, nil
}

func setupTestServer(t *testing.T, server *fakeServer) (*Client, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	financialgatewayv1.RegisterFinancialGatewayServiceServer(srv, server)

	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:             conn,
		financialGateway: financialgatewayv1.NewFinancialGatewayServiceClient(conn),
		timeout:          DefaultTimeout,
	}

	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	}

	return c, cleanup
}

// --- New ---

func TestNew_WithTarget(t *testing.T) {
	c, cleanup, err := New(Config{Target: "localhost:50099"})
	require.NoError(t, err)
	defer cleanup()
	assert.NotNil(t, c)
	assert.NotNil(t, c.Conn())
}

func TestNew_MissingTargetAndServiceName(t *testing.T) {
	_, _, err := New(Config{})
	require.ErrorIs(t, err, ErrTargetRequired)
}

func TestNew_WithServiceName(t *testing.T) {
	// ServiceName-based connection goes through platform factory which needs K8s DNS
	// Just verify it attempts the connection (will fail in test, but exercises code path)
	_, _, err := New(Config{ServiceName: "financial-gateway"})
	// May fail with DNS error but should not be ErrTargetRequired
	if err != nil {
		assert.NotErrorIs(t, err, ErrTargetRequired)
	}
}

// --- DispatchPayment ---

func TestClient_DispatchPayment_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	resp, err := c.DispatchPayment(context.Background(), &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "po-1", resp.GetPaymentOrderId())
	assert.Equal(t, "dispatch-1", resp.GetDispatchId())
}

func TestClient_DispatchPayment_Error(t *testing.T) {
	server := &fakeServer{
		dispatchFn: func(_ context.Context, _ *financialgatewayv1.DispatchPaymentRequest) (*financialgatewayv1.DispatchPaymentResponse, error) {
			return nil, status.Error(codes.Unavailable, "stripe down")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.DispatchPayment(context.Background(), &financialgatewayv1.DispatchPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
}

// --- CancelPayment ---

func TestClient_CancelPayment_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	resp, err := c.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
		Reason:         "test",
	})
	require.NoError(t, err)
	assert.Equal(t, "po-1", resp.GetPaymentOrderId())
	assert.Equal(t, "CANCELLED", resp.GetStatus())
}

func TestClient_CancelPayment_Error(t *testing.T) {
	server := &fakeServer{
		cancelFn: func(_ context.Context, _ *financialgatewayv1.CancelPaymentRequest) (*financialgatewayv1.CancelPaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.CancelPayment(context.Background(), &financialgatewayv1.CancelPaymentRequest{
		PaymentOrderId: "po-1",
	})
	require.Error(t, err)
}

// --- DispatchRefund ---

func TestClient_DispatchRefund_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	resp, err := c.DispatchRefund(context.Background(), &financialgatewayv1.DispatchRefundRequest{
		OriginalDispatchId: "po-1",
		RefundAmountUnits:  5000,
	})
	require.NoError(t, err)
	assert.Equal(t, "refund-1", resp.GetDispatchId())
}

func TestClient_DispatchRefund_Error(t *testing.T) {
	server := &fakeServer{
		refundFn: func(_ context.Context, _ *financialgatewayv1.DispatchRefundRequest) (*financialgatewayv1.DispatchRefundResponse, error) {
			return nil, status.Error(codes.Unimplemented, "not implemented")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.DispatchRefund(context.Background(), &financialgatewayv1.DispatchRefundRequest{
		OriginalDispatchId: "po-1",
	})
	require.Error(t, err)
}

// --- GetProviderHealth ---

func TestClient_GetProviderHealth_Success(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	resp, err := c.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.NoError(t, err)
	assert.Equal(t, financialgatewayv1.ProviderHealth_PROVIDER_HEALTH_HEALTHY, resp.GetHealth())
}

func TestClient_GetProviderHealth_Error(t *testing.T) {
	server := &fakeServer{
		healthFn: func(_ context.Context, _ *financialgatewayv1.GetProviderHealthRequest) (*financialgatewayv1.GetProviderHealthResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
	}
	c, cleanup := setupTestServer(t, server)
	defer cleanup()

	_, err := c.GetProviderHealth(context.Background(), &financialgatewayv1.GetProviderHealthRequest{
		Rail: financialgatewayv1.PaymentRail_PAYMENT_RAIL_STRIPE,
	})
	require.Error(t, err)
}

// --- Close ---

func TestClient_Close(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	err := c.Close()
	require.NoError(t, err)
}

func TestClient_Close_NilConn(t *testing.T) {
	c := &Client{}
	err := c.Close()
	require.NoError(t, err)
}

// --- Conn ---

func TestClient_Conn(t *testing.T) {
	c, cleanup := setupTestServer(t, &fakeServer{})
	defer cleanup()

	conn := c.Conn()
	assert.NotNil(t, conn)
}
