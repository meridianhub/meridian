// Package tools provides the tool registry for the MCP server.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	mcperrors "github.com/meridianhub/meridian/services/mcp-server/internal/errors"
)

// GatewayInstructionQuerier is the minimal interface for listing and retrieving instructions.
type GatewayInstructionQuerier interface {
	ListInstructions(ctx context.Context, req *opgatewayv1.ListInstructionsRequest) (*opgatewayv1.ListInstructionsResponse, error)
	GetInstruction(ctx context.Context, req *opgatewayv1.GetInstructionRequest) (*opgatewayv1.GetInstructionResponse, error)
}

// GatewayConnectionQuerier is the minimal interface for listing and retrieving provider connections.
type GatewayConnectionQuerier interface {
	ListConnections(ctx context.Context, req *opgatewayv1.ListConnectionsRequest) (*opgatewayv1.ListConnectionsResponse, error)
	GetConnection(ctx context.Context, req *opgatewayv1.GetConnectionRequest) (*opgatewayv1.GetConnectionResponse, error)
}

// GatewayInstructionWriter is the minimal interface for mutating instruction state.
type GatewayInstructionWriter interface {
	CancelInstruction(ctx context.Context, req *opgatewayv1.CancelInstructionRequest) (*opgatewayv1.CancelInstructionResponse, error)
}

// GatewayClients groups all client dependencies for gateway tools.
type GatewayClients struct {
	InstructionQuerier GatewayInstructionQuerier
	ConnectionQuerier  GatewayConnectionQuerier
	InstructionWriter  GatewayInstructionWriter
}

// RegisterGatewayTools registers all operational gateway tools onto the SDK server.
// Tools whose required client is nil are silently skipped.
func RegisterGatewayTools(srv *mcp.Server, clients GatewayClients) {
	var candidates []Tool

	if clients.InstructionQuerier != nil {
		candidates = append(candidates, buildGatewayDispatchStatusTool(clients.InstructionQuerier))
		candidates = append(candidates, buildGatewayInstructionDetailTool(clients.InstructionQuerier))
	}
	if clients.ConnectionQuerier != nil {
		candidates = append(candidates, buildGatewayConnectionHealthTool(clients.ConnectionQuerier))
	}
	if clients.InstructionWriter != nil {
		candidates = append(candidates, buildGatewayCancelInstructionTool(clients.InstructionWriter))
	}

	for _, t := range candidates {
		addTool(srv, t)
	}
}

// buildGatewayDispatchStatusTool returns the meridian_gateway_dispatch_status tool.
func buildGatewayDispatchStatusTool(client GatewayInstructionQuerier) Tool {
	return Tool{
		Name:     "meridian_gateway_dispatch_status",
		Category: CategoryRead,
		Description: "List instructions filtered by status, connection, or time range. " +
			"Returns instruction summaries showing dispatch lifecycle state for operational monitoring. " +
			"Use this to inspect pending, failed, or in-flight instructions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"status": map[string]interface{}{
					"type":        "string",
					"description": "Filter by instruction status. One of: PENDING, DISPATCHING, DELIVERED, ACKNOWLEDGED, RETRYING, FAILED, EXPIRED, CANCELLED.",
					"enum":        []interface{}{"PENDING", "DISPATCHING", "DELIVERED", "ACKNOWLEDGED", "RETRYING", "FAILED", "EXPIRED", "CANCELLED"},
				},
				"connection_id": map[string]interface{}{
					"type":        "string",
					"description": "Filter by provider connection UUID (optional).",
					"pattern":     `^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?$`,
				},
				"instruction_type": map[string]interface{}{
					"type":        "string",
					"description": "Filter by instruction type category (optional). Example: \"payment.initiate\", \"kyc.verify\".",
				},
				"from_time": map[string]interface{}{
					"type":        "string",
					"format":      "date-time",
					"description": "Filter instructions created on or after this ISO 8601 timestamp (optional).",
				},
				"to_time": map[string]interface{}{
					"type":        "string",
					"format":      "date-time",
					"description": "Filter instructions created on or before this ISO 8601 timestamp (optional).",
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return (default 50, max 100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleGatewayDispatchStatus(ctx, client, params)
		},
	}
}

// gatewayDispatchStatusParams holds parsed parameters for meridian_gateway_dispatch_status.
type gatewayDispatchStatusParams struct {
	Status          string `json:"status"`
	ConnectionID    string `json:"connection_id"`
	InstructionType string `json:"instruction_type"`
	FromTime        string `json:"from_time"`
	ToTime          string `json:"to_time"`
	PageSize        int32  `json:"page_size"`
}

// handleGatewayDispatchStatus implements the meridian_gateway_dispatch_status handler logic.
func handleGatewayDispatchStatus(ctx context.Context, client GatewayInstructionQuerier, params json.RawMessage) (interface{}, error) {
	var p gatewayDispatchStatusParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	req, validationErr := buildDispatchStatusRequest(p)
	if validationErr != "" {
		return map[string]interface{}{"error": validationErr}, nil
	}

	resp, err := client.ListInstructions(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.Instructions) == 0 {
		return map[string]interface{}{
			"message":      "no instructions found matching the query",
			"instructions": []interface{}{},
		}, nil
	}

	instructions := make([]map[string]interface{}, 0, len(resp.Instructions))
	for _, instr := range resp.Instructions {
		instructions = append(instructions, formatInstructionSummary(instr))
	}

	result := map[string]interface{}{
		"count":        len(instructions),
		"instructions": instructions,
	}
	if resp.Pagination != nil && resp.Pagination.NextPageToken != "" {
		result["next_page_token"] = resp.Pagination.NextPageToken
	}
	return result, nil
}

// buildDispatchStatusRequest constructs the gRPC request from parsed params.
func buildDispatchStatusRequest(p gatewayDispatchStatusParams) (*opgatewayv1.ListInstructionsRequest, string) {
	req := &opgatewayv1.ListInstructionsRequest{}

	if errMsg := applyStatusFilter(p.Status, req); errMsg != "" {
		return nil, errMsg
	}

	if p.ConnectionID != "" {
		req.ProviderConnectionId = p.ConnectionID
	}
	if p.InstructionType != "" {
		req.InstructionType = p.InstructionType
	}

	if errMsg := applyDateRange(p.FromTime, p.ToTime, req); errMsg != "" {
		return nil, errMsg
	}

	pageSize := p.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	req.Pagination = &commonv1.Pagination{PageSize: pageSize}

	return req, ""
}

// applyStatusFilter adds a status filter to the request if p.Status is non-empty.
// Returns a non-empty error message when the status value is invalid.
func applyStatusFilter(statusStr string, req *opgatewayv1.ListInstructionsRequest) string {
	if statusStr == "" {
		return ""
	}
	statusVal := instructionStatusFromString(statusStr)
	if statusVal == opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED {
		return fmt.Sprintf("invalid status %q: must be one of PENDING, DISPATCHING, DELIVERED, ACKNOWLEDGED, RETRYING, FAILED, EXPIRED, CANCELLED", statusStr)
	}
	req.Status = []opgatewayv1.InstructionStatus{statusVal}
	return ""
}

// applyDateRange parses fromStr and toStr as RFC3339 timestamps and adds a
// DateRange to req when at least one is provided.
// Returns a non-empty error message when a value is malformed or the range is inverted.
func applyDateRange(fromStr, toStr string, req *opgatewayv1.ListInstructionsRequest) string {
	var fromTime, toTime time.Time
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return fmt.Sprintf("invalid from_time format (expected RFC3339): %v", err)
		}
		fromTime = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return fmt.Sprintf("invalid to_time format (expected RFC3339): %v", err)
		}
		toTime = t
	}
	if !fromTime.IsZero() && !toTime.IsZero() && fromTime.After(toTime) {
		return "from_time must be before or equal to to_time"
	}
	if !fromTime.IsZero() || !toTime.IsZero() {
		req.DateRange = &commonv1.DateRange{}
		if !fromTime.IsZero() {
			req.DateRange.StartDate = fromTime.Format(time.RFC3339)
		}
		if !toTime.IsZero() {
			req.DateRange.EndDate = toTime.Format(time.RFC3339)
		}
	}
	return ""
}

// buildGatewayConnectionHealthTool returns the meridian_gateway_connection_health tool.
func buildGatewayConnectionHealthTool(client GatewayConnectionQuerier) Tool {
	return Tool{
		Name:     "meridian_gateway_connection_health",
		Category: CategoryRead,
		Description: "Show provider connection health status and configuration. " +
			"Lists all connections with health status when no connection_id is provided, " +
			"or returns detailed info for a specific connection. " +
			"Use this to monitor which provider integrations are healthy, degraded, or unhealthy.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"connection_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of a specific connection to retrieve (optional). Omit to list all connections.",
					"pattern":     `^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})?$`,
				},
				"health_status": map[string]interface{}{
					"type":        "string",
					"description": "Filter list by health status (optional). One of: HEALTHY, DEGRADED, UNHEALTHY.",
					"enum":        []interface{}{"HEALTHY", "DEGRADED", "UNHEALTHY"},
				},
				"page_size": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of results to return when listing (default 25, max 100).",
					"minimum":     1,
					"maximum":     100,
				},
			},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleGatewayConnectionHealth(ctx, client, params)
		},
	}
}

// gatewayConnectionHealthParams holds parsed parameters for meridian_gateway_connection_health.
type gatewayConnectionHealthParams struct {
	ConnectionID string `json:"connection_id"`
	HealthStatus string `json:"health_status"`
	PageSize     int32  `json:"page_size"`
}

// handleGatewayConnectionHealth implements the meridian_gateway_connection_health handler logic.
func handleGatewayConnectionHealth(ctx context.Context, client GatewayConnectionQuerier, params json.RawMessage) (interface{}, error) {
	var p gatewayConnectionHealthParams
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	// If a specific connection_id is requested, return its details.
	if p.ConnectionID != "" {
		resp, err := client.GetConnection(ctx, &opgatewayv1.GetConnectionRequest{
			ConnectionId: p.ConnectionID,
		})
		if err != nil {
			return mcperrors.FormatGRPCError(err), nil
		}
		if resp.Connection == nil {
			return map[string]interface{}{
				"message": fmt.Sprintf("no connection found with id %s", p.ConnectionID),
			}, nil
		}
		return map[string]interface{}{
			"connection": formatConnectionHealth(resp.Connection),
		}, nil
	}

	// Otherwise list connections with optional health filter.
	req := &opgatewayv1.ListConnectionsRequest{}
	if p.HealthStatus != "" {
		statusVal := healthStatusFromString(p.HealthStatus)
		if statusVal == opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED {
			return map[string]interface{}{
				"error": fmt.Sprintf("invalid health_status %q: must be one of HEALTHY, DEGRADED, UNHEALTHY", p.HealthStatus),
			}, nil
		}
		req.HealthStatus = statusVal
	}
	if p.PageSize > 0 {
		req.Pagination = &commonv1.Pagination{PageSize: p.PageSize}
	}

	resp, err := client.ListConnections(ctx, req)
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if len(resp.Connections) == 0 {
		return map[string]interface{}{
			"message":     "no connections found matching the query",
			"connections": []interface{}{},
		}, nil
	}

	connections := make([]map[string]interface{}, 0, len(resp.Connections))
	for _, conn := range resp.Connections {
		connections = append(connections, formatConnectionHealth(conn))
	}

	result := map[string]interface{}{
		"count":       len(connections),
		"connections": connections,
	}
	if resp.Pagination != nil && resp.Pagination.NextPageToken != "" {
		result["next_page_token"] = resp.Pagination.NextPageToken
	}
	return result, nil
}

// buildGatewayInstructionDetailTool returns the meridian_gateway_instruction_detail tool.
func buildGatewayInstructionDetailTool(client GatewayInstructionQuerier) Tool {
	return Tool{
		Name:     "meridian_gateway_instruction_detail",
		Category: CategoryRead,
		Description: "Get detailed instruction information including the full attempt history. " +
			"Returns the instruction payload, metadata, status, and every dispatch attempt with " +
			"response codes and error messages. Use this to investigate failed or stuck instructions.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instruction_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the instruction to retrieve.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
			},
			"required": []interface{}{"instruction_id"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleGatewayInstructionDetail(ctx, client, params)
		},
	}
}

// handleGatewayInstructionDetail implements the meridian_gateway_instruction_detail handler logic.
func handleGatewayInstructionDetail(ctx context.Context, client GatewayInstructionQuerier, params json.RawMessage) (interface{}, error) {
	var p struct {
		InstructionID string `json:"instruction_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	resp, err := client.GetInstruction(ctx, &opgatewayv1.GetInstructionRequest{
		InstructionId: p.InstructionID,
	})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if resp.Instruction == nil {
		return map[string]interface{}{
			"message": fmt.Sprintf("no instruction found with id %s", p.InstructionID),
		}, nil
	}

	return map[string]interface{}{
		"instruction": formatInstructionDetail(resp.Instruction),
	}, nil
}

// buildGatewayCancelInstructionTool returns the meridian_gateway_cancel_instruction tool.
func buildGatewayCancelInstructionTool(client GatewayInstructionWriter) Tool {
	return Tool{
		Name:     "meridian_gateway_cancel_instruction",
		Category: CategoryWrite,
		Description: "Cancel a pending instruction before it is dispatched. " +
			"Only instructions in PENDING status can be cancelled. " +
			"Use this for manual intervention when an instruction must not be dispatched.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instruction_id": map[string]interface{}{
					"type":        "string",
					"description": "UUID of the instruction to cancel.",
					"pattern":     `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
				},
				"cancellation_reason": map[string]interface{}{
					"type":        "string",
					"description": "Reason for cancelling the instruction.",
				},
			},
			"required": []interface{}{"instruction_id"},
		},
		Handler: func(ctx context.Context, params json.RawMessage) (interface{}, error) {
			return handleGatewayCancelInstruction(ctx, client, params)
		},
	}
}

// handleGatewayCancelInstruction implements the meridian_gateway_cancel_instruction handler logic.
func handleGatewayCancelInstruction(ctx context.Context, client GatewayInstructionWriter, params json.RawMessage) (interface{}, error) {
	var p struct {
		InstructionID      string `json:"instruction_id"`
		CancellationReason string `json:"cancellation_reason"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	resp, err := client.CancelInstruction(ctx, &opgatewayv1.CancelInstructionRequest{
		InstructionId:      p.InstructionID,
		CancellationReason: p.CancellationReason,
	})
	if err != nil {
		return mcperrors.FormatGRPCError(err), nil
	}

	if resp.Instruction == nil {
		return map[string]interface{}{
			"message": fmt.Sprintf("no instruction found with id %s", p.InstructionID),
		}, nil
	}

	return map[string]interface{}{
		"instruction": formatInstructionSummary(resp.Instruction),
		"message":     fmt.Sprintf("instruction %s has been cancelled", p.InstructionID),
	}, nil
}
