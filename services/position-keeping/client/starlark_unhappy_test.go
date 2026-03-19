package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelLogHandler_Success(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := cancelLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context:        context.Background(),
		IdempotencyKey: "cancel-key",
		CorrelationID:  uuid.New(),
		KnowledgeAt:    time.Now(),
	}

	params := map[string]any{
		"log_id": "test-log-123",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	assert.True(t, mockServer.updateCalled)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-log-123", resultMap["log_id"])
	assert.Equal(t, "CANCELLED", resultMap["status"])
}

func TestCancelLogHandler_MissingLogID(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := cancelLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log_id")
	assert.False(t, mockServer.updateCalled)
}

func TestCancelLogHandler_ServiceError(t *testing.T) {
	mockServer := &mockPositionKeepingServer{
		shouldError:  true,
		errorMessage: "service unavailable",
	}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := cancelLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"log_id": "test-log-123",
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position_keeping.cancel_log")
}

func TestUpdateLogHandler_MissingLogID(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := updateLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log_id")
}

func TestUpdateLogHandler_ServiceError(t *testing.T) {
	mockServer := &mockPositionKeepingServer{
		shouldError:  true,
		errorMessage: "database timeout",
	}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := updateLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"log_id": "test-log-123",
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position_keeping.update_log")
}

func TestConvertProtoToDecimal_InvalidString(t *testing.T) {
	result := convertProtoToDecimal("not-a-number")
	assert.True(t, result.IsZero())
}

func TestConvertProtoToDecimal_EmptyString(t *testing.T) {
	result := convertProtoToDecimal("")
	assert.True(t, result.IsZero())
}

func TestBoolPtr(t *testing.T) {
	truePtr := boolPtr(true)
	require.NotNil(t, truePtr)
	assert.True(t, *truePtr)

	falsePtr := boolPtr(false)
	require.NotNil(t, falsePtr)
	assert.False(t, *falsePtr)
}

func TestRegisterStarlarkHandlers_DuplicateRegistration(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	registry := saga.NewHandlerRegistry()

	// First registration should succeed
	err := RegisterStarlarkHandlers(registry, client)
	require.NoError(t, err)

	// Second registration should fail (duplicate handler names)
	err = RegisterStarlarkHandlers(registry, client)
	require.Error(t, err)
}

// mockPositionKeepingServerForInitiate is a variant of the mock that returns specific initiate errors
type mockPositionKeepingServerForInitiate struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer
	initiateErr error
}

func (m *mockPositionKeepingServerForInitiate) InitiateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return nil, m.initiateErr
}

func TestInitiateLogHandler_ContextCancelled(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := initiateLogHandler(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	sagaCtx := &saga.StarlarkContext{
		Context: ctx,
	}

	params := map[string]any{
		"account_id": "test-account",
	}

	_, err := handler(sagaCtx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position_keeping.initiate_log")
}

func TestInitiateLogHandler_InvalidAccountIDType(t *testing.T) {
	mockServer := &mockPositionKeepingServer{}
	client, cleanup := setupMockServer(t, mockServer)
	defer cleanup()

	handler := initiateLogHandler(client)

	ctx := &saga.StarlarkContext{
		Context: context.Background(),
	}

	params := map[string]any{
		"account_id": 12345, // Wrong type - should be string
	}

	_, err := handler(ctx, params)
	require.Error(t, err)
	// RequireStringParam should reject non-string values
	fmt.Println("Error:", err)
}
