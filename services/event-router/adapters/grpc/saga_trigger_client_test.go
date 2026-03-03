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
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/event-router/domain"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// mockSagaExecutionServer implements SagaExecutionServiceServer for testing.
type mockSagaExecutionServer struct {
	controlplanev1.UnimplementedSagaExecutionServiceServer

	executeSagaFunc func(ctx context.Context, req *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error)
	callCount       atomic.Int32
}

func (s *mockSagaExecutionServer) ExecuteSaga(ctx context.Context, req *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
	s.callCount.Add(1)
	if s.executeSagaFunc != nil {
		return s.executeSagaFunc(ctx, req)
	}
	return &controlplanev1.ExecuteSagaResponse{
		SagaId:       uuid.New().String(),
		WasDuplicate: false,
	}, nil
}

// startMockSagaServer starts a gRPC server with the mock saga execution implementation.
func startMockSagaServer(t *testing.T, mock *mockSagaExecutionServer) (string, func()) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	controlplanev1.RegisterSagaExecutionServiceServer(server, mock)

	go func() {
		_ = server.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		server.GracefulStop()
	}
}

// createTestSagaTriggerClient creates a SagaTriggerClient connected to the mock server.
func createTestSagaTriggerClient(t *testing.T, addr string) *SagaTriggerClient {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	retryConfig := sharedclients.RetryConfig{
		MaxRetries:          3,
		InitialInterval:     10 * time.Millisecond,
		MaxInterval:         50 * time.Millisecond,
		Multiplier:          2.0,
		RandomizationFactor: 0,
	}

	return &SagaTriggerClient{
		conn:        conn,
		client:      controlplanev1.NewSagaExecutionServiceClient(conn),
		timeout:     5 * time.Second,
		logger:      slog.Default(),
		retryConfig: retryConfig,
	}
}

func TestSagaTriggerClient_TriggerSaga_Success(t *testing.T) {
	expectedSagaID := uuid.New().String()
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			return &controlplanev1.ExecuteSagaResponse{
				SagaId:       expectedSagaID,
				WasDuplicate: false,
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	inputData := map[string]any{
		"tenant_id": "tenant-123",
		"amount":    "100.00",
		"currency":  "GBP",
	}

	sagaID, err := client.TriggerSaga(context.Background(), "energy_settlement", inputData, "idempotency-key-123")

	require.NoError(t, err)
	assert.Equal(t, expectedSagaID, sagaID)
	assert.Equal(t, int32(1), mock.callCount.Load())
}

func TestSagaTriggerClient_TriggerSaga_Duplicate_ReturnsExistingSagaID(t *testing.T) {
	existingSagaID := uuid.New().String()
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			return &controlplanev1.ExecuteSagaResponse{
				SagaId:       existingSagaID,
				WasDuplicate: true,
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	sagaID, err := client.TriggerSaga(context.Background(), "energy_settlement", nil, "duplicate-key")

	require.NoError(t, err)
	assert.Equal(t, existingSagaID, sagaID)
}

func TestSagaTriggerClient_TriggerSaga_InvalidArgument_NoRetry(t *testing.T) {
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			return nil, status.Error(codes.InvalidArgument, "saga_name must not be empty")
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	_, err := client.TriggerSaga(context.Background(), "bad_name", nil, "key-1")

	require.Error(t, err)
	// INVALID_ARGUMENT must not be retried — only 1 call expected
	assert.Equal(t, int32(1), mock.callCount.Load())

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSagaTriggerClient_TriggerSaga_Unavailable_Retries(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			count := callCount.Add(1)
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service temporarily unavailable")
			}
			return &controlplanev1.ExecuteSagaResponse{
				SagaId: uuid.New().String(),
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	sagaID, err := client.TriggerSaga(context.Background(), "energy_settlement", nil, "key-2")

	require.NoError(t, err)
	assert.NotEmpty(t, sagaID)
	// Should have retried: 3 total calls (2 failures + 1 success)
	assert.Equal(t, int32(3), callCount.Load())
}

func TestSagaTriggerClient_TriggerSaga_Internal_Retries(t *testing.T) {
	callCount := &atomic.Int32{}
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			count := callCount.Add(1)
			if count < 2 {
				return nil, status.Error(codes.Internal, "internal server error")
			}
			return &controlplanev1.ExecuteSagaResponse{
				SagaId: uuid.New().String(),
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	sagaID, err := client.TriggerSaga(context.Background(), "energy_settlement", nil, "key-3")

	require.NoError(t, err)
	assert.NotEmpty(t, sagaID)
	// 2 total calls (1 failure + 1 success)
	assert.Equal(t, int32(2), callCount.Load())
}

func TestSagaTriggerClient_TriggerSaga_MaxRetries_Exceeded(t *testing.T) {
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			return nil, status.Error(codes.Unavailable, "service permanently unavailable")
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	_, err := client.TriggerSaga(context.Background(), "energy_settlement", nil, "key-4")

	require.Error(t, err)
	// Initial attempt + 3 retries = 4 total calls
	assert.Equal(t, int32(4), mock.callCount.Load())

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unavailable, st.Code())
}

func TestSagaTriggerClient_TriggerSaga_ContextCancelled(t *testing.T) {
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(ctx context.Context, _ *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				return nil, nil
			}
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.TriggerSaga(ctx, "energy_settlement", nil, "key-5")

	require.Error(t, err)
	assert.LessOrEqual(t, mock.callCount.Load(), int32(2))
}

func TestSagaTriggerClient_TriggerSaga_InputDataConversion(t *testing.T) {
	var capturedReq *controlplanev1.ExecuteSagaRequest
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, req *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			capturedReq = req
			return &controlplanev1.ExecuteSagaResponse{
				SagaId: uuid.New().String(),
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	inputData := map[string]any{
		"tenant_id":    "tenant-abc",
		"amount":       "500.00",
		"event_type":   "ENERGY_CONSUMED",
		"reading_time": "2026-01-01T00:00:00Z",
	}

	_, err := client.TriggerSaga(context.Background(), "energy_settlement", inputData, "idempotency-xyz")

	require.NoError(t, err)
	require.NotNil(t, capturedReq)
	assert.Equal(t, "energy_settlement", capturedReq.SagaName)
	assert.Equal(t, "idempotency-xyz", capturedReq.IdempotencyKey)

	// Verify input data was serialized to structpb
	require.NotNil(t, capturedReq.InputData)
	fields := capturedReq.InputData.GetFields()
	assert.Equal(t, "tenant-abc", fields["tenant_id"].GetStringValue())
	assert.Equal(t, "500.00", fields["amount"].GetStringValue())
	assert.Equal(t, "ENERGY_CONSUMED", fields["event_type"].GetStringValue())
}

func TestSagaTriggerClient_TriggerSaga_NilInputData(t *testing.T) {
	var capturedReq *controlplanev1.ExecuteSagaRequest
	mock := &mockSagaExecutionServer{
		executeSagaFunc: func(_ context.Context, req *controlplanev1.ExecuteSagaRequest) (*controlplanev1.ExecuteSagaResponse, error) {
			capturedReq = req
			return &controlplanev1.ExecuteSagaResponse{
				SagaId: uuid.New().String(),
			}, nil
		},
	}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	defer func() {
		_ = client.Close()
	}()

	// nil inputData should be valid — some sagas take no input
	sagaID, err := client.TriggerSaga(context.Background(), "simple_saga", nil, "key-nil")

	require.NoError(t, err)
	assert.NotEmpty(t, sagaID)
	require.NotNil(t, capturedReq)
	// inputData should be nil or empty struct
	if capturedReq.InputData != nil {
		assert.Empty(t, capturedReq.InputData.GetFields())
	}
}

func TestSagaTriggerClient_buildRequest_ConvertsInputData(t *testing.T) {
	client := &SagaTriggerClient{}

	inputData := map[string]any{
		"string_val": "hello",
		"bool_val":   true,
		"num_val":    float64(42),
	}

	req, err := client.buildRequest("my_saga", inputData, "idem-key")

	require.NoError(t, err)
	assert.Equal(t, "my_saga", req.SagaName)
	assert.Equal(t, "idem-key", req.IdempotencyKey)
	require.NotNil(t, req.InputData)

	fields := req.InputData.GetFields()
	assert.Equal(t, "hello", fields["string_val"].GetStringValue())
	assert.True(t, fields["bool_val"].GetBoolValue())
	assert.Equal(t, float64(42), fields["num_val"].GetNumberValue())
}

func TestSagaTriggerClient_buildRequest_NilInputData(t *testing.T) {
	client := &SagaTriggerClient{}

	req, err := client.buildRequest("my_saga", nil, "idem-key")

	require.NoError(t, err)
	assert.Equal(t, "my_saga", req.SagaName)
	assert.Equal(t, "idem-key", req.IdempotencyKey)
	// nil input data => nil struct (valid for sagas with no input)
	assert.Nil(t, req.InputData)
}

func TestSagaTriggerClient_buildRequest_InvalidInputData(t *testing.T) {
	client := &SagaTriggerClient{}

	// structpb.NewStruct cannot handle non-JSON-compatible types like chan
	inputData := map[string]any{
		"channel": make(chan int), // invalid type
	}

	_, err := client.buildRequest("my_saga", inputData, "idem-key")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid input_data")
}

func TestNewSagaTriggerClient_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		config      *SagaTriggerClientConfig
		wantErr     bool
		errContains string
	}{
		{
			name:        "nil config",
			config:      nil,
			wantErr:     true,
			errContains: "SagaTriggerClientConfig is required",
		},
		{
			name: "missing service name",
			config: &SagaTriggerClientConfig{
				ServiceName: "",
				Port:        50051,
			},
			wantErr:     true,
			errContains: "ServiceName is required",
		},
		{
			name: "valid config",
			config: &SagaTriggerClientConfig{
				ServiceName: "control-plane",
				Port:        50051,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewSagaTriggerClient(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, client)
			_ = client.Close()
		})
	}
}

func TestSagaTriggerClient_Close(t *testing.T) {
	mock := &mockSagaExecutionServer{}
	addr, cleanup := startMockSagaServer(t, mock)
	defer cleanup()

	client := createTestSagaTriggerClient(t, addr)
	err := client.Close()

	require.NoError(t, err)
}

func TestSagaTriggerClient_buildRequest_NestedData(t *testing.T) {
	client := &SagaTriggerClient{}

	inputData := map[string]any{
		"nested": map[string]any{
			"key": "value",
		},
		"list": []any{"a", "b", "c"},
	}

	req, err := client.buildRequest("my_saga", inputData, "idem-key")

	require.NoError(t, err)
	require.NotNil(t, req.InputData)

	fields := req.InputData.GetFields()
	require.NotNil(t, fields["nested"])
	nestedFields := fields["nested"].GetStructValue().GetFields()
	assert.Equal(t, "value", nestedFields["key"].GetStringValue())

	listValues := fields["list"].GetListValue().GetValues()
	require.Len(t, listValues, 3)
	assert.Equal(t, "a", listValues[0].GetStringValue())
}

// Ensure SagaTriggerClient satisfies the domain.SagaTrigger interface at compile time.
var _ domain.SagaTrigger = (*SagaTriggerClient)(nil)

// Ensure structpb is imported (used by buildRequest tests indirectly).
var _ = (*structpb.Struct)(nil)
