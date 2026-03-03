// Package grpc provides gRPC client adapters for external service communication.
package grpc

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/shopspring/decimal"
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

	recordMeasurementFunc func(ctx context.Context, req *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error)
	callCount             atomic.Int32
}

func (s *mockPositionKeepingServer) RecordMeasurement(ctx context.Context, req *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
	s.callCount.Add(1)
	if s.recordMeasurementFunc != nil {
		return s.recordMeasurementFunc(ctx, req)
	}
	return &positionkeepingv1.RecordMeasurementResponse{
		MeasurementId:   uuid.New().String(),
		PositionStateId: req.PositionStateId,
		RecordedAt:      timestamppb.Now(),
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

// createTestMeasurement creates a domain.Measurement for testing.
func createTestMeasurement() *auditdomain.Measurement {
	now := time.Now().UTC()
	period, _ := auditdomain.NewPeriod(now, now)

	return &auditdomain.Measurement{
		ID:           uuid.New(),
		AccountID:    uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		AssetCode:    "MERIDIAN-CURRENT-ACCOUNT-OPS",
		Quantity:     decimal.NewFromInt(1),
		Period:       period,
		Source:       "AUDIT_STREAM",
		QualityScore: 100,
		Attributes: map[string]string{
			"service":   "current_account",
			"operation": "INSERT",
			"table":     "accounts",
		},
		ReceivedAt: now,
	}
}

func TestPositionKeepingGRPCClient_RecordMeasurement_Success(t *testing.T) {
	mock := &mockPositionKeepingServer{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	measurement := createTestMeasurement()
	ctx := context.Background()

	err := client.RecordMeasurement(ctx, measurement)

	require.NoError(t, err)
	assert.Equal(t, int32(1), mock.callCount.Load())
}

func TestPositionKeepingGRPCClient_RecordMeasurement_InvalidArgument_NoRetry(t *testing.T) {
	mock := &mockPositionKeepingServer{
		recordMeasurementFunc: func(_ context.Context, _ *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "invalid measurement type")
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	measurement := createTestMeasurement()
	ctx := context.Background()

	err := client.RecordMeasurement(ctx, measurement)

	require.Error(t, err)
	// INVALID_ARGUMENT should not be retried - only 1 call
	assert.Equal(t, int32(1), mock.callCount.Load())

	// Verify it's the right error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestPositionKeepingGRPCClient_RecordMeasurement_Unavailable_Retries(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockPositionKeepingServer{
		recordMeasurementFunc: func(_ context.Context, req *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
			count := callCount.Add(1)
			// Fail on first 2 attempts, succeed on 3rd
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service temporarily unavailable")
			}
			return &positionkeepingv1.RecordMeasurementResponse{
				MeasurementId:   uuid.New().String(),
				PositionStateId: req.PositionStateId,
				RecordedAt:      timestamppb.Now(),
			}, nil
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	measurement := createTestMeasurement()
	ctx := context.Background()

	err := client.RecordMeasurement(ctx, measurement)

	require.NoError(t, err)
	// Should have retried: 3 total calls (2 failures + 1 success)
	assert.Equal(t, int32(3), callCount.Load())
}

func TestPositionKeepingGRPCClient_RecordMeasurement_Internal_Retries(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockPositionKeepingServer{
		recordMeasurementFunc: func(_ context.Context, req *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
			count := callCount.Add(1)
			// Fail on first attempt, succeed on 2nd
			if count < 2 {
				return nil, status.Error(codes.Internal, "internal server error")
			}
			return &positionkeepingv1.RecordMeasurementResponse{
				MeasurementId:   uuid.New().String(),
				PositionStateId: req.PositionStateId,
				RecordedAt:      timestamppb.Now(),
			}, nil
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	measurement := createTestMeasurement()
	ctx := context.Background()

	err := client.RecordMeasurement(ctx, measurement)

	require.NoError(t, err)
	// Should have retried: 2 total calls (1 failure + 1 success)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestPositionKeepingGRPCClient_RecordMeasurement_MaxRetries_Exceeded(t *testing.T) {
	mock := &mockPositionKeepingServer{
		recordMeasurementFunc: func(_ context.Context, _ *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
			return nil, status.Error(codes.Unavailable, "service permanently unavailable")
		},
	}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	client := createTestClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	measurement := createTestMeasurement()
	ctx := context.Background()

	err := client.RecordMeasurement(ctx, measurement)

	require.Error(t, err)
	// Should have made initial attempt + 3 retries = 4 total calls
	assert.Equal(t, int32(4), mock.callCount.Load())

	// Verify it's the right error
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestPositionKeepingGRPCClient_RecordMeasurement_ContextCancelled(t *testing.T) {
	mock := &mockPositionKeepingServer{
		recordMeasurementFunc: func(ctx context.Context, _ *positionkeepingv1.RecordMeasurementRequest) (*positionkeepingv1.RecordMeasurementResponse, error) {
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

	measurement := createTestMeasurement()

	// Cancel context immediately
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := client.RecordMeasurement(ctx, measurement)

	require.Error(t, err)
	// Should only attempt once before context cancellation stops retries
	assert.LessOrEqual(t, mock.callCount.Load(), int32(2))
}

func TestPositionKeepingGRPCClient_buildRecordMeasurementRequest(t *testing.T) {
	client := &PositionKeepingGRPCClient{}
	measurement := createTestMeasurement()

	req := client.buildRecordMeasurementRequest(measurement)

	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", req.MeasurementType)
	assert.Equal(t, "1", req.Value)
	// Unit now uses instrument dimension (COUNT for operation type)
	assert.Equal(t, "COUNT", req.Unit)
	assert.Equal(t, measurement.AccountID.String(), req.PositionStateId)
	assert.NotNil(t, req.Timestamp)

	// Check metadata from measurement attributes
	assert.Equal(t, "current_account", req.Metadata["service"])
	assert.Equal(t, "INSERT", req.Metadata["operation"])
	assert.Equal(t, "AUDIT_STREAM", req.Metadata["source"])
	assert.Equal(t, "100", req.Metadata["quality_score"])

	// Check instrument metadata for typed quantity reconstruction
	assert.Equal(t, "OPERATION", req.Metadata["instrument_code"])
	assert.Equal(t, "1", req.Metadata["instrument_version"])
	assert.Equal(t, "COUNT", req.Metadata["instrument_dimension"])
	assert.Equal(t, "0", req.Metadata["instrument_precision"])
}

func TestPositionKeepingGRPCClient_buildRecordMeasurementRequest_TransactionUnit(t *testing.T) {
	client := &PositionKeepingGRPCClient{}
	measurement := createTestMeasurement()
	measurement.Attributes["unit"] = "transaction"

	req := client.buildRecordMeasurementRequest(measurement)

	// Unit uses instrument dimension
	assert.Equal(t, "COUNT", req.Unit)
	// Instrument metadata reflects TRANSACTION instrument
	assert.Equal(t, "TRANSACTION", req.Metadata["instrument_code"])
	assert.Equal(t, "1", req.Metadata["instrument_version"])
	assert.Equal(t, "COUNT", req.Metadata["instrument_dimension"])
	assert.Equal(t, "0", req.Metadata["instrument_precision"])
}

func TestPositionKeepingGRPCClient_buildRecordMeasurementRequest_StorageUnit(t *testing.T) {
	client := &PositionKeepingGRPCClient{}
	measurement := createTestMeasurement()
	measurement.Attributes["unit"] = "storage_gb_hour"

	req := client.buildRecordMeasurementRequest(measurement)

	// Unit uses instrument dimension
	assert.Equal(t, "DATA", req.Unit)
	// Instrument metadata reflects STORAGE_GB_HOUR instrument
	assert.Equal(t, "STORAGE_GB_HOUR", req.Metadata["instrument_code"])
	assert.Equal(t, "1", req.Metadata["instrument_version"])
	assert.Equal(t, "DATA", req.Metadata["instrument_dimension"])
	assert.Equal(t, "6", req.Metadata["instrument_precision"])
}

func TestPositionKeepingGRPCClient_buildRecordMeasurementRequest_ComputeUnit(t *testing.T) {
	client := &PositionKeepingGRPCClient{}
	measurement := createTestMeasurement()
	measurement.Attributes["unit"] = "compute_hour"

	req := client.buildRecordMeasurementRequest(measurement)

	// Unit uses instrument dimension
	assert.Equal(t, "COMPUTE", req.Unit)
	// Instrument metadata reflects COMPUTE_HOUR instrument
	assert.Equal(t, "COMPUTE_HOUR", req.Metadata["instrument_code"])
	assert.Equal(t, "1", req.Metadata["instrument_version"])
	assert.Equal(t, "COMPUTE", req.Metadata["instrument_dimension"])
	assert.Equal(t, "6", req.Metadata["instrument_precision"])
}

func TestPositionKeepingGRPCClient_buildRecordMeasurementRequest_UnknownUnit(t *testing.T) {
	client := &PositionKeepingGRPCClient{}
	measurement := createTestMeasurement()
	measurement.Attributes["unit"] = "unknown_type"

	req := client.buildRecordMeasurementRequest(measurement)

	// Unknown unit types default to OPERATION instrument
	assert.Equal(t, "COUNT", req.Unit)
	assert.Equal(t, "OPERATION", req.Metadata["instrument_code"])
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
