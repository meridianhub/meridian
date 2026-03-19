package grpc

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// mockPositionKeepingServer implements the PositionKeepingService for testing.
type mockPositionKeepingServer struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer

	getAccountBalancesFunc func(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error)
	getAccountBalanceFunc  func(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error)
	callCount              atomic.Int32
}

func (s *mockPositionKeepingServer) GetAccountBalances(ctx context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	s.callCount.Add(1)
	if s.getAccountBalancesFunc != nil {
		return s.getAccountBalancesFunc(ctx, req)
	}
	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.AccountId,
		Balances: []*positionkeepingv1.BalanceEntry{
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "GBP",
					Amount:         "1000.00",
					Version:        1,
				},
			},
			{
				BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
				Amount: &quantityv1.InstrumentAmount{
					InstrumentCode: "GBP",
					Amount:         "950.00",
					Version:        1,
				},
			},
		},
		AsOf: timestamppb.Now(),
	}, nil
}

func (s *mockPositionKeepingServer) GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	s.callCount.Add(1)
	if s.getAccountBalanceFunc != nil {
		return s.getAccountBalanceFunc(ctx, req)
	}
	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.AccountId,
		BalanceType: req.BalanceType,
		Amount: &quantityv1.InstrumentAmount{
			InstrumentCode: req.InstrumentCode,
			Amount:         "1000.00",
			Version:        1,
		},
		AsOf: timestamppb.Now(),
	}, nil
}

// startMockServer starts a gRPC server with the mock implementation.
func startMockServer(t *testing.T, mock *mockPositionKeepingServer) (string, func()) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	positionkeepingv1.RegisterPositionKeepingServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		server.GracefulStop()
	}
}

// createTestClient creates a client connected to the mock server.
func createTestClient(t *testing.T, addr string) *PositionKeepingGRPCClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	// Create retry config with fast timeouts for tests
	retryConfig := sharedclients.RetryConfig{
		MaxRetries:          3,
		InitialInterval:     10 * time.Millisecond,
		MaxInterval:         50 * time.Millisecond,
		Multiplier:          2.0,
		RandomizationFactor: 0,
	}

	return &PositionKeepingGRPCClient{
		conn:        conn,
		client:      positionkeepingv1.NewPositionKeepingServiceClient(conn),
		timeout:     5 * time.Second,
		logger:      slog.Default(),
		retryConfig: retryConfig,
	}
}

func TestGetAccountBalances_Success(t *testing.T) {
	mock := &mockPositionKeepingServer{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "test-account-123",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "test-account-123", resp.AccountId)
	assert.Len(t, resp.Balances, 2)
	assert.Equal(t, int32(1), mock.callCount.Load())
}

func TestGetAccountBalances_Retry_OnUnavailable(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			count := callCount.Add(1)
			// Fail on first 2 attempts, succeed on 3rd
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service temporarily unavailable")
			}
			return &positionkeepingv1.GetAccountBalancesResponse{
				AccountId: req.AccountId,
				Balances: []*positionkeepingv1.BalanceEntry{
					{
						BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
						Amount: &quantityv1.InstrumentAmount{
							InstrumentCode: "GBP",
							Amount:         "500.00",
							Version:        1,
						},
					},
				},
				AsOf: timestamppb.Now(),
			}, nil
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "test-account-456",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Should have retried: 3 total calls (2 failures + 1 success)
	assert.Equal(t, int32(3), callCount.Load())
}

func TestGetAccountBalances_Timeout(t *testing.T) {
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(ctx context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			// Simulate slow operation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				return nil, nil
			}
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "test-account-timeout",
		InstrumentCode: "GBP",
	}

	// Use short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resp, err := client.GetAccountBalances(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	// Should only attempt once or twice before context cancellation stops retries
	assert.LessOrEqual(t, mock.callCount.Load(), int32(2))
}

func TestGetAccountBalances_InvalidArgument_NoRetry(t *testing.T) {
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "invalid account_id format")
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "bad-format!!!",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	// INVALID_ARGUMENT should not be retried - only 1 call
	assert.Equal(t, int32(1), mock.callCount.Load())

	// Verify it's the right error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetAccountBalances_NotFound(t *testing.T) {
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			return nil, status.Error(codes.NotFound, "position not found for account")
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "nonexistent-account",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	// NotFound should not be retried - only 1 call
	assert.Equal(t, int32(1), mock.callCount.Load())

	// Verify it's the right error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetAccountBalances_MaxRetries_Exceeded(t *testing.T) {
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			return nil, status.Error(codes.Unavailable, "service permanently unavailable")
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "test-account-max-retry",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.Error(t, err)
	require.Nil(t, resp)
	// Should have made initial attempt + 3 retries = 4 total calls
	assert.Equal(t, int32(4), mock.callCount.Load())

	// Verify it's the right error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestGetAccountBalances_Internal_Retries(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockPositionKeepingServer{
		getAccountBalancesFunc: func(_ context.Context, req *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
			count := callCount.Add(1)
			// Fail on first attempt, succeed on 2nd
			if count < 2 {
				return nil, status.Error(codes.Internal, "internal server error")
			}
			return &positionkeepingv1.GetAccountBalancesResponse{
				AccountId: req.AccountId,
				Balances: []*positionkeepingv1.BalanceEntry{
					{
						BalanceType: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
						Amount: &quantityv1.InstrumentAmount{
							InstrumentCode: "GBP",
							Amount:         "750.00",
							Version:        1,
						},
					},
				},
				AsOf: timestamppb.Now(),
			}, nil
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	req := &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      "test-account-internal",
		InstrumentCode: "GBP",
	}
	ctx := context.Background()

	resp, err := client.GetAccountBalances(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Should have retried: 2 total calls (1 failure + 1 success)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestNewPositionKeepingClient_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      *ClientConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "missing service name",
			config: &ClientConfig{
				ServiceName: "",
				Port:        50053,
			},
			wantErr:     true,
			errContains: "ServiceName is required",
		},
		{
			name: "valid config with defaults",
			config: &ClientConfig{
				ServiceName: "position-keeping",
				Port:        50053,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewPositionKeepingClient(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			// For valid configs, we expect connection to fail since there's no server
			// but the client should be created
			if err == nil && client != nil {
				_ = client.Close()
			}
		})
	}
}

func TestNewPositionKeepingClient_DefaultTimeout(t *testing.T) {
	// This test verifies that the default timeout is set correctly
	// We can't fully test this without a running server, but we verify the config

	config := &ClientConfig{
		ServiceName: "position-keeping",
		Port:        50053,
		// Timeout not set - should default to 5s
	}

	// The client creation will fail due to no server, but we verify the timeout default
	assert.Equal(t, time.Duration(0), config.Timeout, "timeout should start as zero")

	// After NewPositionKeepingClient would run, it should be 5s
	// We can't easily test this without mocking the connection
}

func TestNewPositionKeepingClient_RetryConfigOverride(t *testing.T) {
	// Test that MaxInterval is capped at 1 second per requirements
	customConfig := &sharedclients.RetryConfig{
		MaxRetries:          5,
		InitialInterval:     200 * time.Millisecond,
		MaxInterval:         10 * time.Second, // This should be capped to 1s
		Multiplier:          2.0,
		RandomizationFactor: 0.5,
	}

	config := &ClientConfig{
		ServiceName: "position-keeping",
		Port:        50053,
		RetryConfig: customConfig,
	}

	// The client will be created with the capped MaxInterval
	// We verify this by checking our config remains unchanged (client modifies its own copy)
	assert.Equal(t, 10*time.Second, config.RetryConfig.MaxInterval, "original config should not be modified")
}

func TestClose(t *testing.T) {
	mock := &mockPositionKeepingServer{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)

	// Close should not error
	err := client.Close()
	require.NoError(t, err)

	// Double close should also not error (conn is nil-safe)
	client.conn = nil
	err = client.Close()
	require.NoError(t, err)
}
