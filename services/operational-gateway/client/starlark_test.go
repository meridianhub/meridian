package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

const bufSize = 1024 * 1024

// mockOperationalGatewayServer implements OperationalGatewayServiceServer for testing.
type mockOperationalGatewayServer struct {
	opgatewayv1.UnimplementedOperationalGatewayServiceServer

	dispatchCalled bool
	cancelCalled   bool
	getCalled      bool

	lastDispatchReq *opgatewayv1.DispatchInstructionRequest
	lastCancelReq   *opgatewayv1.CancelInstructionRequest
}

func (m *mockOperationalGatewayServer) DispatchInstruction(_ context.Context, req *opgatewayv1.DispatchInstructionRequest) (*opgatewayv1.DispatchInstructionResponse, error) {
	m.dispatchCalled = true
	m.lastDispatchReq = req

	return &opgatewayv1.DispatchInstructionResponse{
		Instruction: &opgatewayv1.Instruction{
			Id:              "test-instruction-id",
			InstructionType: req.InstructionType,
			Status:          opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
			Priority:        opgatewayv1.Priority_PRIORITY_NORMAL,
		},
	}, nil
}

func (m *mockOperationalGatewayServer) CancelInstruction(_ context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
	m.cancelCalled = true
	m.lastCancelReq = req

	return &opgatewayv1.CancelInstructionResponse{
		Instruction: &opgatewayv1.Instruction{
			Id:     req.InstructionId,
			Status: opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED,
		},
	}, nil
}

func (m *mockOperationalGatewayServer) GetInstruction(_ context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
	m.getCalled = true

	return &opgatewayv1.GetInstructionResponse{
		Instruction: &opgatewayv1.Instruction{
			Id:              req.InstructionId,
			InstructionType: "payment.collect",
			Status:          opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED,
		},
	}, nil
}

// setupTestClient creates an in-memory gRPC client/server pair for testing.
func setupTestClient(t *testing.T) (*Client, *mockOperationalGatewayServer, func()) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mock := &mockOperationalGatewayServer{}
	opgatewayv1.RegisterOperationalGatewayServiceServer(srv, mock)

	go func() {
		_ = srv.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	c := &Client{
		conn:               conn,
		operationalGateway: opgatewayv1.NewOperationalGatewayServiceClient(conn),
		timeout:            10 * time.Second,
	}

	cleanup := func() {
		conn.Close()
		srv.Stop()
		lis.Close()
	}

	return c, mock, cleanup
}

// newTestStarlarkContext creates a minimal StarlarkContext for testing.
func newTestStarlarkContext() *saga.StarlarkContext {
	return &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		IdempotencyKey:  "test-idempotency-key",
		LookupCache:     saga.NewLookupResultCache(),
	}
}

func TestRegisterStarlarkHandlers(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	err := RegisterStarlarkHandlers(registry, c)
	require.NoError(t, err)

	handlers := registry.List()
	assert.Len(t, handlers, 3)

	expectedHandlers := []string{
		"operational_gateway.dispatch_instruction",
		"operational_gateway.cancel_instruction",
		"operational_gateway.get_instruction",
	}
	for _, name := range expectedHandlers {
		_, err := registry.Get(name)
		require.NoError(t, err, "handler %q should be registered", name)
	}
}

func TestDispatchInstructionHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.dispatch_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instruction_type": "payment.collect",
		"payload": map[string]any{
			"account_id": "acc-123",
			"amount":     "100.00",
		},
		"priority":       "HIGH",
		"correlation_id": "corr-abc",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	assert.True(t, mock.dispatchCalled)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-instruction-id", resultMap["instruction_id"])
	assert.Equal(t, "PENDING", resultMap["status"])

	// Verify idempotency key was propagated
	assert.Equal(t, "test-idempotency-key", mock.lastDispatchReq.GetIdempotencyKey().GetKey())
	assert.Equal(t, "payment.collect", mock.lastDispatchReq.GetInstructionType())
	assert.Equal(t, opgatewayv1.Priority_PRIORITY_HIGH, mock.lastDispatchReq.GetPriority())
	assert.Equal(t, "corr-abc", mock.lastDispatchReq.GetCorrelationId())
}

func TestDispatchInstructionHandlerMissingType(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.dispatch_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"payload": map[string]any{"key": "value"},
	}

	_, err = handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instruction_type")
}

func TestDispatchInstructionHandlerMissingPayload(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.dispatch_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instruction_type": "payment.collect",
	}

	_, err = handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payload")
}

func TestDispatchInstructionHandlerScheduledAt(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.dispatch_instruction")
	require.NoError(t, err)

	scheduledAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instruction_type": "payment.collect",
		"payload":          map[string]any{"key": "value"},
		"scheduled_at":     scheduledAt,
	}

	_, err = handler(ctx, params)
	require.NoError(t, err)
	assert.NotNil(t, mock.lastDispatchReq.GetScheduledAt())
}

func TestCancelInstructionHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.cancel_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instruction_id": "inst-xyz",
		"reason":         "saga_compensation",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	assert.True(t, mock.cancelCalled)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inst-xyz", resultMap["instruction_id"])
	assert.Equal(t, "CANCELLED", resultMap["status"])
	assert.Equal(t, "saga_compensation", mock.lastCancelReq.GetCancellationReason())
}

func TestCancelInstructionHandlerMissingID(t *testing.T) {
	c, _, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.cancel_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()

	_, err = handler(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instruction_id")
}

func TestGetInstructionHandler(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.get_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	params := map[string]any{
		"instruction_id": "inst-abc",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)
	assert.True(t, mock.getCalled)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "inst-abc", resultMap["instruction_id"])
	assert.Equal(t, "payment.collect", resultMap["instruction_type"])
	assert.Equal(t, "DELIVERED", resultMap["status"])
}

func TestDispatchInstructionHandlerIdempotencyKey(t *testing.T) {
	c, mock, cleanup := setupTestClient(t)
	defer cleanup()

	registry := saga.NewHandlerRegistry()
	require.NoError(t, RegisterStarlarkHandlers(registry, c))

	handler, err := registry.Get("operational_gateway.dispatch_instruction")
	require.NoError(t, err)

	ctx := newTestStarlarkContext()
	ctx.IdempotencyKey = "unique-saga-step-key"
	params := map[string]any{
		"instruction_type": "notification.sms",
		"payload":          map[string]any{"phone": "+1234567890"},
	}

	_, err = handler(ctx, params)
	require.NoError(t, err)

	// The idempotency key from the StarlarkContext must be used.
	assert.Equal(t, "unique-saga-step-key", mock.lastDispatchReq.GetIdempotencyKey().GetKey())
}

// Ensure the package-level common types compile correctly.
var (
	_ *commonv1.IdempotencyKey = &commonv1.IdempotencyKey{}
	_ *structpb.Struct         = &structpb.Struct{}
)
