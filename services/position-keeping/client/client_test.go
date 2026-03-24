package client_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/position-keeping/client"
)

// mockPositionKeepingServer implements the PositionKeepingServiceServer interface for testing.
type mockPositionKeepingServer struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer

	// GetAccountBalance behavior
	getAccountBalanceResp *positionkeepingv1.GetAccountBalanceResponse
	getAccountBalanceErr  error

	// GetAccountBalances behavior
	getAccountBalancesResp *positionkeepingv1.GetAccountBalancesResponse
	getAccountBalancesErr  error
}

func (m *mockPositionKeepingServer) GetAccountBalance(
	_ context.Context,
	_ *positionkeepingv1.GetAccountBalanceRequest,
) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	if m.getAccountBalanceErr != nil {
		return nil, m.getAccountBalanceErr
	}
	return m.getAccountBalanceResp, nil
}

func (m *mockPositionKeepingServer) GetAccountBalances(
	_ context.Context,
	_ *positionkeepingv1.GetAccountBalancesRequest,
) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if m.getAccountBalancesErr != nil {
		return nil, m.getAccountBalancesErr
	}
	return m.getAccountBalancesResp, nil
}

// setupTestServer creates a gRPC server with the mock service and returns client config.
func setupTestServer(t *testing.T, mock *mockPositionKeepingServer) (client.Config, func()) {
	t.Helper()

	// Create a listener on a random port
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "localhost:0")
	require.NoError(t, err)

	// Create and register the gRPC server
	grpcServer := grpc.NewServer()
	positionkeepingv1.RegisterPositionKeepingServiceServer(grpcServer, mock)

	// Start serving in a goroutine
	go func() {
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("gRPC server error: %v", err)
		}
	}()

	cleanup := func() {
		grpcServer.GracefulStop()
	}

	return client.Config{
		Target:  lis.Addr().String(),
		Timeout: 5 * time.Second,
		DialOptions: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}, cleanup
}

func TestGetAccountBalance_Success(t *testing.T) {
	// Arrange
	now := time.Now()
	mock := &mockPositionKeepingServer{
		getAccountBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			AccountId:   "acc-123",
			BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			Amount: &quantityv1.InstrumentAmount{
				Amount:         "100.50",
				InstrumentCode: "GBP",
				Version:        1,
			},
			AsOf: timestamppb.New(now),
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalance(context.Background(), &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "acc-123",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-123", resp.AccountId)
	assert.Equal(t, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, resp.BalanceType)
	assert.Equal(t, "GBP", resp.Amount.InstrumentCode)
	assert.Equal(t, "100.50", resp.Amount.Amount)
	assert.Equal(t, int32(1), resp.Amount.Version)
}

func TestGetAccountBalance_NotFound(t *testing.T) {
	// Arrange
	mock := &mockPositionKeepingServer{
		getAccountBalanceErr: status.Error(codes.NotFound, "account not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalance(context.Background(), &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "nonexistent",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	})

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get account balance")

	// Verify underlying gRPC status
	st, ok := status.FromError(errors.Unwrap(err))
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetAccountBalance_ServerError(t *testing.T) {
	// Arrange
	mock := &mockPositionKeepingServer{
		getAccountBalanceErr: status.Error(codes.Internal, "database error"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalance(context.Background(), &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "acc-123",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	})

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get account balance")
}

func TestGetAccountBalances_Success(t *testing.T) {
	// Arrange
	now := time.Now()
	mock := &mockPositionKeepingServer{
		getAccountBalancesResp: &positionkeepingv1.GetAccountBalancesResponse{
			AccountId: "acc-123",
			Balances: []*positionkeepingv1.BalanceEntry{
				{
					BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
					Amount: &quantityv1.InstrumentAmount{
						Amount:         "1000.00",
						InstrumentCode: "GBP",
						Version:        1,
					},
				},
				{
					BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
					Amount: &quantityv1.InstrumentAmount{
						Amount:         "800.00",
						InstrumentCode: "GBP",
						Version:        1,
					},
				},
			},
			AsOf: timestamppb.New(now),
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalances(context.Background(), &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "acc-123",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-123", resp.AccountId)
	require.Len(t, resp.Balances, 2)

	// Check current balance
	assert.Equal(t, positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT, resp.Balances[0].BalanceType)
	assert.Equal(t, "1000.00", resp.Balances[0].Amount.Amount)

	// Check available balance
	assert.Equal(t, positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE, resp.Balances[1].BalanceType)
	assert.Equal(t, "800.00", resp.Balances[1].Amount.Amount)
}

func TestGetAccountBalances_NotFound(t *testing.T) {
	// Arrange
	mock := &mockPositionKeepingServer{
		getAccountBalancesErr: status.Error(codes.NotFound, "account not found"),
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalances(context.Background(), &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "nonexistent",
	})

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get account balances")
}

func TestGetAccountBalances_EmptyResponse(t *testing.T) {
	// Arrange
	now := time.Now()
	mock := &mockPositionKeepingServer{
		getAccountBalancesResp: &positionkeepingv1.GetAccountBalancesResponse{
			AccountId: "acc-new",
			Balances:  []*positionkeepingv1.BalanceEntry{}, // No balances yet
			AsOf:      timestamppb.New(now),
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act
	resp, err := c.GetAccountBalances(context.Background(), &positionkeepingv1.GetAccountBalancesRequest{
		AccountId: "acc-new",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-new", resp.AccountId)
	assert.Empty(t, resp.Balances)
}

func TestGetAccountBalance_ContextCanceled(t *testing.T) {
	// Arrange
	mock := &mockPositionKeepingServer{
		getAccountBalanceResp: &positionkeepingv1.GetAccountBalanceResponse{
			AccountId: "acc-123",
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Create an already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	resp, err := c.GetAccountBalance(ctx, &positionkeepingv1.GetAccountBalanceRequest{
		AccountId:   "acc-123",
		BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
	})

	// Assert
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetAccountBalances_WithInstrumentFilter(t *testing.T) {
	// Arrange
	now := time.Now()
	mock := &mockPositionKeepingServer{
		getAccountBalancesResp: &positionkeepingv1.GetAccountBalancesResponse{
			AccountId: "acc-123",
			Balances: []*positionkeepingv1.BalanceEntry{
				{
					BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
					Amount: &quantityv1.InstrumentAmount{
						Amount:         "500.00",
						InstrumentCode: "USD",
						Version:        1,
					},
				},
			},
			AsOf: timestamppb.New(now),
		},
	}

	cfg, cleanup := setupTestServer(t, mock)
	defer cleanup()

	c, clientCleanup, err := client.New(context.Background(), cfg)
	require.NoError(t, err)
	defer clientCleanup()

	// Act - request with instrument code filter
	resp, err := c.GetAccountBalances(context.Background(), &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "acc-123",
		InstrumentCode: "USD",
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "acc-123", resp.AccountId)
	require.Len(t, resp.Balances, 1)
	assert.Equal(t, "USD", resp.Balances[0].Amount.InstrumentCode)
}
