package clients

import (
	"context"
	"testing"

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
	assert.Contains(t, err.Error(), "saga payment_execution v1 not found")
}
