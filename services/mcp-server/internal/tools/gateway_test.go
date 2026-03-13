// Package tools provides the tool registry for the MCP server.
package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// --- Mock implementations ---

type mockGatewayInstructionQuerier struct {
	listInstructionsFn func(ctx context.Context, req *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error)
	getInstructionFn   func(ctx context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error)
}

func (m *mockGatewayInstructionQuerier) ListInstructions(ctx context.Context, req *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
	return m.listInstructionsFn(ctx, req)
}

func (m *mockGatewayInstructionQuerier) GetInstruction(ctx context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
	return m.getInstructionFn(ctx, req)
}

type mockGatewayConnectionQuerier struct {
	listConnectionsFn func(ctx context.Context, req *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error)
	getConnectionFn   func(ctx context.Context, req *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error)
}

func (m *mockGatewayConnectionQuerier) ListConnections(ctx context.Context, req *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
	return m.listConnectionsFn(ctx, req)
}

func (m *mockGatewayConnectionQuerier) GetConnection(ctx context.Context, req *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error) {
	return m.getConnectionFn(ctx, req)
}

type mockGatewayInstructionWriter struct {
	cancelInstructionFn func(ctx context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error)
}

func (m *mockGatewayInstructionWriter) CancelInstruction(ctx context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
	return m.cancelInstructionFn(ctx, req)
}

// --- helper to call a tool by name ---

func callGatewayTool(t *testing.T, clients tools.GatewayClients, toolName string, params interface{}) map[string]interface{} {
	t.Helper()
	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), clients)

	raw, err := json.Marshal(params)
	require.NoError(t, err)

	result, err := reg.Call(context.Background(), toolName, raw)
	require.NoError(t, err)

	out, err := json.Marshal(result)
	require.NoError(t, err)

	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(out, &m))
	return m
}

// --- meridian_gateway_dispatch_status tests ---

func TestGatewayDispatchStatus_NoFilter_ReturnsInstructions(t *testing.T) {
	now := timestamppb.Now()
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			return &opgatewayv1.ListInstructionsResponse{
				Instructions: []*opgatewayv1.Instruction{
					{
						Id:                   "550e8400-e29b-41d4-a716-446655440000",
						InstructionType:      "payment.initiate",
						ProviderConnectionId: "650e8400-e29b-41d4-a716-446655440001",
						Status:               opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
						Priority:             opgatewayv1.Priority_PRIORITY_NORMAL,
						CreatedAt:            now,
					},
				},
			}, nil
		},
		getInstructionFn: nil,
	}

	result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock}, "meridian_gateway_dispatch_status", map[string]interface{}{})

	assert.EqualValues(t, 1, result["count"])
	instructions := result["instructions"].([]interface{})
	require.Len(t, instructions, 1)
	instr := instructions[0].(map[string]interface{})
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", instr["id"])
	assert.Equal(t, "payment.initiate", instr["instruction_type"])
	assert.Equal(t, "PENDING", instr["status"])
}

func TestGatewayDispatchStatus_StatusFilter_PassedToRequest(t *testing.T) {
	var capturedReq *opgatewayv1.ListInstructionsRequest
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, req *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			capturedReq = req
			return &opgatewayv1.ListInstructionsResponse{}, nil
		},
	}

	result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock}, "meridian_gateway_dispatch_status", map[string]interface{}{
		"status": "FAILED",
	})

	assert.Equal(t, "no instructions found matching the query", result["message"])
	require.NotNil(t, capturedReq)
	require.Len(t, capturedReq.Status, 1)
	assert.Equal(t, opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED, capturedReq.Status[0])
}

func TestGatewayDispatchStatus_InvalidStatus_FailsSchemaValidation(t *testing.T) {
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			return &opgatewayv1.ListInstructionsResponse{}, nil
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{InstructionQuerier: mock})

	raw, err := json.Marshal(map[string]interface{}{"status": "BOGUS_STATUS"})
	require.NoError(t, err)

	_, callErr := reg.Call(context.Background(), "meridian_gateway_dispatch_status", raw)
	require.Error(t, callErr, "invalid enum value should fail schema validation")
	assert.Contains(t, callErr.Error(), "validation error")
}

func TestGatewayDispatchStatus_InvalidTimeRange_ReturnsError(t *testing.T) {
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			return &opgatewayv1.ListInstructionsResponse{}, nil
		},
	}

	result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock}, "meridian_gateway_dispatch_status", map[string]interface{}{
		"from_time": "2026-01-10T00:00:00Z",
		"to_time":   "2026-01-01T00:00:00Z",
	})

	assert.Contains(t, result["error"], "from_time must be before or equal to to_time")
}

func TestGatewayDispatchStatus_GRPCError_ReturnsFormattedError(t *testing.T) {
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			return nil, status.Error(codes.Internal, "database unavailable")
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{InstructionQuerier: mock})

	result, err := reg.Call(context.Background(), "meridian_gateway_dispatch_status", json.RawMessage(`{}`))
	require.NoError(t, err, "gRPC errors should be returned as formatted result, not error")
	require.NotNil(t, result)
	// Verify serialized form has valid: false
	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, false, m["valid"])
}

// --- meridian_gateway_connection_health tests ---

func TestGatewayConnectionHealth_ListAll_ReturnsConnections(t *testing.T) {
	now := timestamppb.Now()
	mock := &mockGatewayConnectionQuerier{
		listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
			return &opgatewayv1.ListConnectionsResponse{
				Connections: []*opgatewayv1.ProviderConnection{
					{
						ConnectionId: "650e8400-e29b-41d4-a716-446655440001",
						ProviderName: "Stripe",
						ProviderType: "payment_gateway",
						Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
						BaseUrl:      "https://api.stripe.com",
						HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
						CreatedAt:    now,
						AuthConfig: &opgatewayv1.ProviderConnection_ApiKey{
							ApiKey: &opgatewayv1.ApiKeyAuth{
								HeaderName: "X-API-Key",
								SecretRef:  "stripe-api-key",
							},
						},
					},
				},
			}, nil
		},
		getConnectionFn: nil,
	}

	result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock}, "meridian_gateway_connection_health", map[string]interface{}{})

	assert.EqualValues(t, 1, result["count"])
	connections := result["connections"].([]interface{})
	require.Len(t, connections, 1)
	conn := connections[0].(map[string]interface{})
	assert.Equal(t, "650e8400-e29b-41d4-a716-446655440001", conn["connection_id"])
	assert.Equal(t, "Stripe", conn["provider_name"])
	assert.Equal(t, "HEALTHY", conn["health_status"])
	assert.Equal(t, "api_key", conn["auth_method"])
}

func TestGatewayConnectionHealth_SpecificID_CallsGetConnection(t *testing.T) {
	connectionID := "650e8400-e29b-41d4-a716-446655440001"
	mock := &mockGatewayConnectionQuerier{
		listConnectionsFn: nil,
		getConnectionFn: func(_ context.Context, req *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error) {
			assert.Equal(t, connectionID, req.ConnectionId)
			return &opgatewayv1.GetConnectionResponse{
				Connection: &opgatewayv1.ProviderConnection{
					ConnectionId: connectionID,
					ProviderName: "Stripe",
					Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
					BaseUrl:      "https://api.stripe.com",
					HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_DEGRADED,
					AuthConfig: &opgatewayv1.ProviderConnection_Basic{
						Basic: &opgatewayv1.BasicAuth{
							Username:          "user",
							PasswordSecretRef: "stripe-password",
						},
					},
				},
			}, nil
		},
	}

	result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock}, "meridian_gateway_connection_health", map[string]interface{}{
		"connection_id": connectionID,
	})

	connResult := result["connection"].(map[string]interface{})
	assert.Equal(t, connectionID, connResult["connection_id"])
	assert.Equal(t, "DEGRADED", connResult["health_status"])
	assert.Equal(t, "basic", connResult["auth_method"])
}

func TestGatewayConnectionHealth_InvalidHealthStatus_FailsSchemaValidation(t *testing.T) {
	mock := &mockGatewayConnectionQuerier{
		listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
			return &opgatewayv1.ListConnectionsResponse{}, nil
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{ConnectionQuerier: mock})

	raw, err := json.Marshal(map[string]interface{}{"health_status": "INVALID"})
	require.NoError(t, err)

	_, callErr := reg.Call(context.Background(), "meridian_gateway_connection_health", raw)
	require.Error(t, callErr, "invalid enum value should fail schema validation")
	assert.Contains(t, callErr.Error(), "validation error")
}

func TestGatewayConnectionHealth_GRPCError_ReturnsFormattedError(t *testing.T) {
	mock := &mockGatewayConnectionQuerier{
		listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{ConnectionQuerier: mock})

	result, err := reg.Call(context.Background(), "meridian_gateway_connection_health", json.RawMessage(`{}`))
	require.NoError(t, err, "gRPC errors should be returned as formatted result, not error")
	require.NotNil(t, result)
	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, false, m["valid"])
}

// --- meridian_gateway_instruction_detail tests ---

func TestGatewayInstructionDetail_ValidID_ReturnsDetailWithAttempts(t *testing.T) {
	instructionID := "550e8400-e29b-41d4-a716-446655440000"
	now := timestamppb.Now()
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: nil,
		getInstructionFn: func(_ context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
			assert.Equal(t, instructionID, req.InstructionId)
			return &opgatewayv1.GetInstructionResponse{
				Instruction: &opgatewayv1.Instruction{
					Id:                   instructionID,
					InstructionType:      "payment.initiate",
					ProviderConnectionId: "650e8400-e29b-41d4-a716-446655440001",
					Status:               opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED,
					Priority:             opgatewayv1.Priority_PRIORITY_HIGH,
					CreatedAt:            now,
					Attempts: []*opgatewayv1.InstructionAttempt{
						{
							AttemptNumber:      1,
							DispatchedAt:       now,
							ResponseStatusCode: 500,
							ErrorMessage:       "internal server error",
							DurationMs:         250,
						},
					},
				},
			}, nil
		},
	}

	result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock}, "meridian_gateway_instruction_detail", map[string]interface{}{
		"instruction_id": instructionID,
	})

	instr := result["instruction"].(map[string]interface{})
	assert.Equal(t, instructionID, instr["id"])
	assert.Equal(t, "FAILED", instr["status"])
	assert.Equal(t, "HIGH", instr["priority"])
	attempts := instr["attempts"].([]interface{})
	require.Len(t, attempts, 1)
	attempt := attempts[0].(map[string]interface{})
	assert.EqualValues(t, 1, attempt["attempt_number"])
	assert.EqualValues(t, 500, attempt["response_status_code"])
	assert.Equal(t, "internal server error", attempt["error_message"])
}

func TestGatewayInstructionDetail_NotFound_ReturnsFormattedError(t *testing.T) {
	instructionID := "550e8400-e29b-41d4-a716-446655440000"
	mock := &mockGatewayInstructionQuerier{
		getInstructionFn: func(_ context.Context, _ *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
			return nil, status.Error(codes.NotFound, "instruction not found")
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{InstructionQuerier: mock})

	raw, err := json.Marshal(map[string]interface{}{"instruction_id": instructionID})
	require.NoError(t, err)

	result, callErr := reg.Call(context.Background(), "meridian_gateway_instruction_detail", raw)
	require.NoError(t, callErr, "gRPC errors should be returned as formatted result, not error")
	require.NotNil(t, result)
	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, false, m["valid"])
}

// --- meridian_gateway_cancel_instruction tests ---

func TestGatewayCancelInstruction_ValidID_CancelsInstruction(t *testing.T) {
	instructionID := "550e8400-e29b-41d4-a716-446655440000"
	now := timestamppb.Now()
	mock := &mockGatewayInstructionWriter{
		cancelInstructionFn: func(_ context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
			assert.Equal(t, instructionID, req.InstructionId)
			assert.Equal(t, "duplicate request", req.CancellationReason)
			return &opgatewayv1.CancelInstructionResponse{
				Instruction: &opgatewayv1.Instruction{
					Id:                   instructionID,
					InstructionType:      "payment.initiate",
					ProviderConnectionId: "650e8400-e29b-41d4-a716-446655440001",
					Status:               opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED,
					Priority:             opgatewayv1.Priority_PRIORITY_NORMAL,
					CreatedAt:            now,
				},
			}, nil
		},
	}

	result := callGatewayTool(t, tools.GatewayClients{InstructionWriter: mock}, "meridian_gateway_cancel_instruction", map[string]interface{}{
		"instruction_id":      instructionID,
		"cancellation_reason": "duplicate request",
	})

	assert.Contains(t, result["message"], instructionID)
	assert.Contains(t, result["message"], "cancelled")
	instr := result["instruction"].(map[string]interface{})
	assert.Equal(t, "CANCELLED", instr["status"])
}

func TestGatewayCancelInstruction_FailedPrecondition_ReturnsFormattedError(t *testing.T) {
	instructionID := "550e8400-e29b-41d4-a716-446655440000"
	mock := &mockGatewayInstructionWriter{
		cancelInstructionFn: func(_ context.Context, _ *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "instruction is not in PENDING status")
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{InstructionWriter: mock})

	raw, err := json.Marshal(map[string]interface{}{"instruction_id": instructionID})
	require.NoError(t, err)

	result, callErr := reg.Call(context.Background(), "meridian_gateway_cancel_instruction", raw)
	require.NoError(t, callErr, "gRPC errors should be returned as formatted result, not error")
	require.NotNil(t, result)
	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, false, m["valid"])
}

// --- RegisterGatewayTools nil-skipping tests ---

func TestRegisterGatewayTools_NilClients_SkipsRegistration(t *testing.T) {
	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{})

	toolList := reg.List(context.Background())
	assert.Empty(t, toolList, "no tools should be registered when all clients are nil")
}

func TestRegisterGatewayTools_OnlyInstructionQuerier_RegistersReadTools(t *testing.T) {
	mock := &mockGatewayInstructionQuerier{
		listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
			return &opgatewayv1.ListInstructionsResponse{}, nil
		},
		getInstructionFn: func(_ context.Context, _ *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
			return &opgatewayv1.GetInstructionResponse{}, nil
		},
	}

	reg := newTestServer(t)
	tools.RegisterGatewayTools(reg.Server(), tools.GatewayClients{InstructionQuerier: mock})

	toolList := reg.List(context.Background())
	names := make([]string, len(toolList))
	for i, t := range toolList {
		names[i] = t.Name
	}
	assert.Contains(t, names, "meridian_gateway_dispatch_status")
	assert.Contains(t, names, "meridian_gateway_instruction_detail")
	assert.NotContains(t, names, "meridian_gateway_connection_health")
	assert.NotContains(t, names, "meridian_gateway_cancel_instruction")
}
