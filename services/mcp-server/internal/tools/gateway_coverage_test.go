// Package tools provides the tool registry for the MCP server.
package tools_test

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "google.golang.org/protobuf/types/known/timestamppb"

    commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
    opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
    "github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

// --- Auth method coverage ---

func TestGatewayConnectionHealth_AuthMethods(t *testing.T) {
    tests := []struct {
        name      string
        makeConn  func() *opgatewayv1.ProviderConnection
        wantAuth  string
    }{
        {
            name: "oauth2",
            makeConn: func() *opgatewayv1.ProviderConnection {
                return &opgatewayv1.ProviderConnection{
                    ConnectionId: "650e8400-e29b-41d4-a716-446655440002",
                    ProviderName: "Provider",
                    Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                    HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                    AuthConfig: &opgatewayv1.ProviderConnection_Oauth2{
                        Oauth2: &opgatewayv1.OAuth2Auth{
                            TokenUrl:        "https://auth.example.com/token",
                            ClientSecretRef: "oauth-secret",
                        },
                    },
                }
            },
            wantAuth: "oauth2",
        },
        {
            name: "hmac",
            makeConn: func() *opgatewayv1.ProviderConnection {
                return &opgatewayv1.ProviderConnection{
                    ConnectionId: "650e8400-e29b-41d4-a716-446655440003",
                    ProviderName: "Provider",
                    Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                    HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                    AuthConfig: &opgatewayv1.ProviderConnection_Hmac{
                        Hmac: &opgatewayv1.HMACAuth{
                            Algorithm: "SHA256",
                            SecretRef: "hmac-key",
                        },
                    },
                }
            },
            wantAuth: "hmac",
        },
        {
            name: "mtls",
            makeConn: func() *opgatewayv1.ProviderConnection {
                return &opgatewayv1.ProviderConnection{
                    ConnectionId: "650e8400-e29b-41d4-a716-446655440004",
                    ProviderName: "Provider",
                    Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                    HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                    AuthConfig: &opgatewayv1.ProviderConnection_Mtls{
                        Mtls: &opgatewayv1.MTLSAuth{
                            ClientCertSecretRef: "client-cert",
                            ClientKeySecretRef:  "client-key",
                        },
                    },
                }
            },
            wantAuth: "mtls",
        },
        {
            name: "unknown (nil auth config)",
            makeConn: func() *opgatewayv1.ProviderConnection {
                return &opgatewayv1.ProviderConnection{
                    ConnectionId: "650e8400-e29b-41d4-a716-446655440005",
                    ProviderName: "Provider",
                    Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                    HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                    // AuthConfig intentionally unset
                }
            },
            wantAuth: "unknown",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            conn := tt.makeConn()
            mock := &mockGatewayConnectionQuerier{
                getConnectionFn: func(_ context.Context, _ *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error) {
                    return &opgatewayv1.GetConnectionResponse{Connection: conn}, nil
                },
            }

            result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock},
                "meridian_gateway_connection_health",
                map[string]interface{}{"connection_id": conn.ConnectionId})

            connResult := result["connection"].(map[string]interface{})
            assert.Equal(t, tt.wantAuth, connResult["auth_method"])
        })
    }
}

// --- Protocol string coverage ---

func TestGatewayConnectionHealth_Protocols(t *testing.T) {
    tests := []struct {
        name         string
        protocol     opgatewayv1.Protocol
        wantProtocol string
    }{
        {name: "GRPC", protocol: opgatewayv1.Protocol_PROTOCOL_GRPC, wantProtocol: "GRPC"},
        {name: "WEBHOOK", protocol: opgatewayv1.Protocol_PROTOCOL_WEBHOOK, wantProtocol: "WEBHOOK"},
        {name: "MQTT", protocol: opgatewayv1.Protocol_PROTOCOL_MQTT, wantProtocol: "MQTT"},
        {name: "AMQP", protocol: opgatewayv1.Protocol_PROTOCOL_AMQP, wantProtocol: "AMQP"},
        {name: "UNSPECIFIED", protocol: opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED, wantProtocol: "UNSPECIFIED"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            mock := &mockGatewayConnectionQuerier{
                listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
                    return &opgatewayv1.ListConnectionsResponse{
                        Connections: []*opgatewayv1.ProviderConnection{
                            {
                                ConnectionId: "conn-123",
                                ProviderName: "Provider",
                                Protocol:     tt.protocol,
                                HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                            },
                        },
                    }, nil
                },
            }

            result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock},
                "meridian_gateway_connection_health", map[string]interface{}{})

            connections := result["connections"].([]interface{})
            require.Len(t, connections, 1)
            conn := connections[0].(map[string]interface{})
            assert.Equal(t, tt.wantProtocol, conn["protocol"])
        })
    }
}

// --- InstructionStatus string coverage ---

func TestGatewayDispatchStatus_InstructionStatusStrings(t *testing.T) {
    tests := []struct {
        protoStatus opgatewayv1.InstructionStatus
        wantString  string
    }{
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING, "DISPATCHING"},
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED, "DELIVERED"},
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED, "ACKNOWLEDGED"},
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING, "RETRYING"},
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED, "EXPIRED"},
        {opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED, "UNSPECIFIED"},
    }

    for _, tt := range tests {
        t.Run(tt.wantString, func(t *testing.T) {
            mock := &mockGatewayInstructionQuerier{
                listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
                    return &opgatewayv1.ListInstructionsResponse{
                        Instructions: []*opgatewayv1.Instruction{
                            {
                                Id:     "instr-001",
                                Status: tt.protoStatus,
                            },
                        },
                    }, nil
                },
            }

            result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
                "meridian_gateway_dispatch_status", map[string]interface{}{})

            instructions := result["instructions"].([]interface{})
            require.Len(t, instructions, 1)
            instr := instructions[0].(map[string]interface{})
            assert.Equal(t, tt.wantString, instr["status"])
        })
    }
}

// --- Priority string coverage ---

func TestGatewayDispatchStatus_PriorityStrings(t *testing.T) {
    tests := []struct {
        priority   opgatewayv1.Priority
        wantString string
    }{
        {opgatewayv1.Priority_PRIORITY_LOW, "LOW"},
        {opgatewayv1.Priority_PRIORITY_CRITICAL, "CRITICAL"},
        {opgatewayv1.Priority_PRIORITY_UNSPECIFIED, "UNSPECIFIED"},
    }

    for _, tt := range tests {
        t.Run(tt.wantString, func(t *testing.T) {
            mock := &mockGatewayInstructionQuerier{
                listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
                    return &opgatewayv1.ListInstructionsResponse{
                        Instructions: []*opgatewayv1.Instruction{
                            {
                                Id:       "instr-001",
                                Priority: tt.priority,
                            },
                        },
                    }, nil
                },
            }

            result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
                "meridian_gateway_dispatch_status", map[string]interface{}{})

            instructions := result["instructions"].([]interface{})
            require.Len(t, instructions, 1)
            instr := instructions[0].(map[string]interface{})
            assert.Equal(t, tt.wantString, instr["priority"])
        })
    }
}

// --- Pagination token coverage ---

func TestGatewayDispatchStatus_PaginationToken_IncludedInResponse(t *testing.T) {
    mock := &mockGatewayInstructionQuerier{
        listInstructionsFn: func(_ context.Context, _ *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
            return &opgatewayv1.ListInstructionsResponse{
                Instructions: []*opgatewayv1.Instruction{
                    {Id: "instr-001", Status: opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING},
                },
                Pagination: &commonv1.PaginationResponse{
                    NextPageToken: "next-page-cursor-abc",
                },
            }, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
        "meridian_gateway_dispatch_status", map[string]interface{}{})

    assert.Equal(t, "next-page-cursor-abc", result["next_page_token"])
    assert.EqualValues(t, 1, result["count"])
}

// --- GetConnection nil response coverage ---

func TestGatewayConnectionHealth_GetConnection_NilResponse(t *testing.T) {
    connectionID := "650e8400-e29b-41d4-a716-446655440009"
    mock := &mockGatewayConnectionQuerier{
        getConnectionFn: func(_ context.Context, req *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error) {
            assert.Equal(t, connectionID, req.ConnectionId)
            return &opgatewayv1.GetConnectionResponse{Connection: nil}, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock},
        "meridian_gateway_connection_health",
        map[string]interface{}{"connection_id": connectionID})

    assert.Contains(t, result["message"], connectionID)
    assert.Nil(t, result["connection"])
}

// --- CancelInstruction nil response coverage ---

func TestGatewayCancelInstruction_NilInstructionResponse(t *testing.T) {
    instructionID := "550e8400-e29b-41d4-a716-446655440099"
    mock := &mockGatewayInstructionWriter{
        cancelInstructionFn: func(_ context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error) {
            assert.Equal(t, instructionID, req.InstructionId)
            return &opgatewayv1.CancelInstructionResponse{Instruction: nil}, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{InstructionWriter: mock},
        "meridian_gateway_cancel_instruction",
        map[string]interface{}{"instruction_id": instructionID})

    assert.Contains(t, result["message"], instructionID)
    assert.Nil(t, result["instruction"])
}

// --- InstructionDetail nil instruction response coverage ---

func TestGatewayInstructionDetail_NilInstructionResponse(t *testing.T) {
    instructionID := "550e8400-e29b-41d4-a716-446655440098"
    mock := &mockGatewayInstructionQuerier{
        getInstructionFn: func(_ context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
            assert.Equal(t, instructionID, req.InstructionId)
            return &opgatewayv1.GetInstructionResponse{Instruction: nil}, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
        "meridian_gateway_instruction_detail",
        map[string]interface{}{"instruction_id": instructionID})

    assert.Contains(t, result["message"], instructionID)
    assert.Nil(t, result["instruction"])
}

// --- Valid from_time/to_time are forwarded to gRPC request ---

func TestGatewayDispatchStatus_ValidTimeRange_AppliedToRequest(t *testing.T) {
    var capturedReq *opgatewayv1.ListInstructionsRequest
    mock := &mockGatewayInstructionQuerier{
        listInstructionsFn: func(_ context.Context, req *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error) {
            capturedReq = req
            return &opgatewayv1.ListInstructionsResponse{}, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
        "meridian_gateway_dispatch_status", map[string]interface{}{
            "from_time": "2026-01-01T00:00:00Z",
            "to_time":   "2026-01-10T00:00:00Z",
        })

    assert.Equal(t, "no instructions found matching the query", result["message"])
    require.NotNil(t, capturedReq)
    require.NotNil(t, capturedReq.DateRange)
    assert.Equal(t, "2026-01-01T00:00:00Z", capturedReq.DateRange.StartDate)
    assert.Equal(t, "2026-01-10T00:00:00Z", capturedReq.DateRange.EndDate)
}

// --- Connection health list with pagination token ---

func TestGatewayConnectionHealth_ListWithPaginationToken(t *testing.T) {
    mock := &mockGatewayConnectionQuerier{
        listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
            return &opgatewayv1.ListConnectionsResponse{
                Connections: []*opgatewayv1.ProviderConnection{
                    {
                        ConnectionId: "conn-001",
                        ProviderName: "Provider",
                        Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                        HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                    },
                },
                Pagination: &commonv1.PaginationResponse{
                    NextPageToken: "conn-next-page",
                },
            }, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock},
        "meridian_gateway_connection_health", map[string]interface{}{})

    assert.Equal(t, "conn-next-page", result["next_page_token"])
    assert.EqualValues(t, 1, result["count"])
}

// --- Connection health with retry policy and rate limit coverage ---

func TestGatewayConnectionHealth_RetryPolicyAndRateLimit(t *testing.T) {
    mock := &mockGatewayConnectionQuerier{
        listConnectionsFn: func(_ context.Context, _ *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error) {
            return &opgatewayv1.ListConnectionsResponse{
                Connections: []*opgatewayv1.ProviderConnection{
                    {
                        ConnectionId: "conn-retry",
                        ProviderName: "Provider",
                        Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
                        HealthStatus: opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY,
                        RetryPolicy: &opgatewayv1.RetryPolicy{
                            MaxAttempts:            3,
                            InitialBackoffSeconds:  1,
                            MaxBackoffSeconds:      30,
                            BackoffMultiplier:      2.0,
                        },
                        RateLimit: &opgatewayv1.RateLimit{
                            RequestsPerSecond: 100,
                            BurstSize:         200,
                        },
                        LastHealthCheckAt: timestamppb.Now(),
                    },
                },
            }, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{ConnectionQuerier: mock},
        "meridian_gateway_connection_health", map[string]interface{}{})

    connections := result["connections"].([]interface{})
    require.Len(t, connections, 1)
    conn := connections[0].(map[string]interface{})

    retryPolicy := conn["retry_policy"].(map[string]interface{})
    assert.EqualValues(t, 3, retryPolicy["max_attempts"])
    assert.EqualValues(t, 1, retryPolicy["initial_backoff_seconds"])
    assert.EqualValues(t, 30, retryPolicy["max_backoff_seconds"])

    rateLimit := conn["rate_limit"].(map[string]interface{})
    assert.EqualValues(t, 100, rateLimit["requests_per_second"])
    assert.EqualValues(t, 200, rateLimit["burst_size"])

    assert.NotEmpty(t, conn["last_health_check_at"])
}

// --- Instruction detail with metadata and optional fields coverage ---

func TestGatewayInstructionDetail_WithMetadataAndScheduling(t *testing.T) {
    instructionID := "550e8400-e29b-41d4-a716-446655440088"
    now := timestamppb.Now()
    mock := &mockGatewayInstructionQuerier{
        getInstructionFn: func(_ context.Context, _ *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error) {
            return &opgatewayv1.GetInstructionResponse{
                Instruction: &opgatewayv1.Instruction{
                    Id:              instructionID,
                    InstructionType: "payment.initiate",
                    Status:          opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING,
                    CorrelationId:   "corr-abc",
                    CausationId:     "cause-xyz",
                    ScheduledAt:     now,
                    ExpiresAt:       now,
                    UpdatedAt:       now,
                    Metadata: map[string]string{
                        "reference": "REF-001",
                    },
                    Attempts: []*opgatewayv1.InstructionAttempt{
                        {
                            AttemptNumber:       1,
                            DispatchedAt:        now,
                            ResponseStatusCode:  200,
                            DurationMs:          150,
                            ResponseBodyPreview: `{"status":"ok"}`,
                        },
                    },
                },
            }, nil
        },
    }

    result := callGatewayTool(t, tools.GatewayClients{InstructionQuerier: mock},
        "meridian_gateway_instruction_detail",
        map[string]interface{}{"instruction_id": instructionID})

    instr := result["instruction"].(map[string]interface{})
    assert.Equal(t, "corr-abc", instr["correlation_id"])
    assert.Equal(t, "cause-xyz", instr["causation_id"])
    assert.NotEmpty(t, instr["scheduled_at"])
    assert.NotEmpty(t, instr["expires_at"])
    assert.NotEmpty(t, instr["updated_at"])

    metadata := instr["metadata"].(map[string]interface{})
    assert.Equal(t, "REF-001", metadata["reference"])

    attempts := instr["attempts"].([]interface{})
    require.Len(t, attempts, 1)
    attempt := attempts[0].(map[string]interface{})
    assert.Equal(t, `{"status":"ok"}`, attempt["response_body_preview"])
}
