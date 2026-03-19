package clients

import (
	"context"
	"testing"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockSagaRegistryClient is a mock implementation of SagaRegistryServiceClient.
type mockSagaRegistryClient struct {
	sagav1.SagaRegistryServiceClient
	getSagaFunc func(ctx context.Context, in *sagav1.GetSagaRequest, opts ...grpc.CallOption) (*sagav1.GetSagaResponse, error)
}

func (m *mockSagaRegistryClient) GetSaga(ctx context.Context, in *sagav1.GetSagaRequest, opts ...grpc.CallOption) (*sagav1.GetSagaResponse, error) {
	if m.getSagaFunc != nil {
		return m.getSagaFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestReferenceDataClientWrapper_GetSaga_Success(t *testing.T) {
	// Arrange
	mockClient := &mockSagaRegistryClient{
		getSagaFunc: func(_ context.Context, in *sagav1.GetSagaRequest, _ ...grpc.CallOption) (*sagav1.GetSagaResponse, error) {
			assert.Equal(t, "payment_execution", in.Name)
			assert.Equal(t, int32(1), in.Version)

			return &sagav1.GetSagaResponse{
				Saga: &sagav1.SagaDefinition{
					Id:      "test-saga-id",
					Name:    "payment_execution",
					Version: 1,
					Script:  "def payment_execution(): return {'status': 'success'}",
					Status:  sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				},
			}, nil
		},
	}

	client := &ReferenceDataClientWrapper{
		sagaClient: mockClient,
	}

	// Act
	result, err := client.GetSaga(context.Background(), "payment_execution", 1)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "test-saga-id", result.ID)
	assert.Equal(t, "payment_execution", result.Name)
	assert.Equal(t, 1, result.Version)
	assert.Equal(t, "def payment_execution(): return {'status': 'success'}", result.Script)
	assert.Equal(t, "SAGA_STATUS_ACTIVE", result.Status)
}

func TestReferenceDataClientWrapper_GetSaga_ActiveVersion(t *testing.T) {
	// Arrange
	mockClient := &mockSagaRegistryClient{
		getSagaFunc: func(_ context.Context, in *sagav1.GetSagaRequest, _ ...grpc.CallOption) (*sagav1.GetSagaResponse, error) {
			assert.Equal(t, "payment_execution", in.Name)
			assert.Equal(t, int32(0), in.Version) // 0 means get active version

			return &sagav1.GetSagaResponse{
				Saga: &sagav1.SagaDefinition{
					Id:      "active-saga-id",
					Name:    "payment_execution",
					Version: 2, // Active is version 2
					Script:  "def payment_execution(): return {'status': 'active'}",
					Status:  sagav1.SagaStatus_SAGA_STATUS_ACTIVE,
				},
			}, nil
		},
	}

	client := &ReferenceDataClientWrapper{
		sagaClient: mockClient,
	}

	// Act
	result, err := client.GetSaga(context.Background(), "payment_execution", 0)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 2, result.Version)
	assert.Equal(t, "SAGA_STATUS_ACTIVE", result.Status)
}

func TestReferenceDataClientWrapper_GetSaga_NotFound(t *testing.T) {
	// Arrange
	mockClient := &mockSagaRegistryClient{
		getSagaFunc: func(_ context.Context, _ *sagav1.GetSagaRequest, _ ...grpc.CallOption) (*sagav1.GetSagaResponse, error) {
			return nil, status.Error(codes.NotFound, "saga not found")
		},
	}

	client := &ReferenceDataClientWrapper{
		sagaClient: mockClient,
	}

	// Act
	result, err := client.GetSaga(context.Background(), "nonexistent_saga", 1)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get saga nonexistent_saga v1")
}

func TestReferenceDataClientWrapper_GetSaga_NilResponse(t *testing.T) {
	// Arrange
	mockClient := &mockSagaRegistryClient{
		getSagaFunc: func(_ context.Context, _ *sagav1.GetSagaRequest, _ ...grpc.CallOption) (*sagav1.GetSagaResponse, error) {
			return &sagav1.GetSagaResponse{Saga: nil}, nil
		},
	}

	client := &ReferenceDataClientWrapper{
		sagaClient: mockClient,
	}

	// Act
	result, err := client.GetSaga(context.Background(), "payment_execution", 1)

	// Assert
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "saga not found: payment_execution v1")
}

// mockReferenceDataServiceClient is a mock implementation of ReferenceDataServiceClient.
type mockReferenceDataServiceClient struct {
	referencedatav1.ReferenceDataServiceClient
	retrieveInstrumentFunc func(ctx context.Context, in *referencedatav1.RetrieveInstrumentRequest, opts ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error)
}

func (m *mockReferenceDataServiceClient) RetrieveInstrument(ctx context.Context, in *referencedatav1.RetrieveInstrumentRequest, opts ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
	if m.retrieveInstrumentFunc != nil {
		return m.retrieveInstrumentFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestReferenceDataClientWrapper_RetrieveInstrument_Success(t *testing.T) {
	mockClient := &mockReferenceDataServiceClient{
		retrieveInstrumentFunc: func(_ context.Context, in *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
			assert.Equal(t, "USD", in.Code)
			assert.Equal(t, int32(0), in.Version)

			return &referencedatav1.RetrieveInstrumentResponse{
				Instrument: &referencedatav1.InstrumentDefinition{
					Code:                     "USD",
					Version:                  1,
					FungibilityKeyExpression: "instrument_code + ':' + string(version)",
				},
			}, nil
		},
	}

	client := &ReferenceDataClientWrapper{
		instrumentClient: mockClient,
	}

	result, err := client.RetrieveInstrument(context.Background(), "USD")

	require.NoError(t, err)
	assert.Equal(t, "USD", result.Code)
	assert.Equal(t, int32(1), result.Version)
	assert.Equal(t, "instrument_code + ':' + string(version)", result.FungibilityKeyExpression)
}

func TestReferenceDataClientWrapper_RetrieveInstrument_NotFound(t *testing.T) {
	mockClient := &mockReferenceDataServiceClient{
		retrieveInstrumentFunc: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return nil, status.Error(codes.NotFound, "instrument not found")
		},
	}

	client := &ReferenceDataClientWrapper{
		instrumentClient: mockClient,
	}

	result, err := client.RetrieveInstrument(context.Background(), "NONEXISTENT")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to retrieve instrument NONEXISTENT")
}

func TestReferenceDataClientWrapper_RetrieveInstrument_NilResponse(t *testing.T) {
	mockClient := &mockReferenceDataServiceClient{
		retrieveInstrumentFunc: func(_ context.Context, _ *referencedatav1.RetrieveInstrumentRequest, _ ...grpc.CallOption) (*referencedatav1.RetrieveInstrumentResponse, error) {
			return &referencedatav1.RetrieveInstrumentResponse{Instrument: nil}, nil
		},
	}

	client := &ReferenceDataClientWrapper{
		instrumentClient: mockClient,
	}

	result, err := client.RetrieveInstrument(context.Background(), "USD")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrInstrumentNotFound)
}

func TestNewReferenceDataClient_NilConn(t *testing.T) {
	// NewReferenceDataClient with nil conn should not panic
	// (gRPC clients are lazy - they don't connect until a call is made)
	client := NewReferenceDataClient(nil)
	require.NotNil(t, client)
}

func TestReferenceDataClientWrapper_Close_NilConn(t *testing.T) {
	client := &ReferenceDataClientWrapper{
		conn: nil,
	}

	err := client.Close()
	assert.NoError(t, err)
}
