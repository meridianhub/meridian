// Package client provides Starlark service bindings for OperationalGateway.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls,
// enabling saga scripts to dispatch instructions to external providers.
package client

import (
	"context"
	"fmt"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// contextKey is a type for context keys to avoid collisions.
type contextKey string

// correlationIDContextKey is the typed context key for correlation ID.
const correlationIDContextKey contextKey = "x-correlation-id"

// RegisterStarlarkHandlers registers all Starlark service bindings for OperationalGateway.
// These handlers adapt the Starlark interface (map[string]any) to gRPC client calls.
//
// This function is called during service initialization to register OperationalGateway
// handlers with the saga execution engine. Registered handlers:
//   - operational_gateway.dispatch_instruction
//   - operational_gateway.cancel_instruction
//   - operational_gateway.get_instruction
//
// Example usage:
//
//	registry := saga.NewHandlerRegistry()
//	client, cleanup, _ := client.New(client.Config{...})
//	defer cleanup()
//	err := RegisterStarlarkHandlers(registry, client)
func RegisterStarlarkHandlers(registry *saga.HandlerRegistry, c *Client) error {
	handlers := map[string]struct {
		handler  saga.Handler
		metadata saga.HandlerMetadata
	}{
		"operational_gateway.dispatch_instruction": {
			handler: dispatchInstructionHandler(c),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		"operational_gateway.cancel_instruction": {
			handler: cancelInstructionHandler(c),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
		"operational_gateway.get_instruction": {
			handler: getInstructionHandler(c),
			metadata: saga.HandlerMetadata{
				Category:            saga.HandlerCategorySettlement,
				ProducesInstruments: []string{},
			},
		},
	}

	for name, h := range handlers {
		if err := registry.RegisterWithMetadata(name, h.handler, &h.metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", name, err)
		}
	}
	return nil
}

// dispatchInstructionHandler queues an instruction for dispatch to an external provider.
//
// Parameters:
//   - instruction_type (string, required): The category of operation (e.g., "payment.collect")
//   - payload (map, required): Instruction-specific data sent to the provider
//   - priority (string, optional): Dispatch priority: LOW, NORMAL, HIGH, CRITICAL (default: NORMAL)
//   - correlation_id (string, optional): Links to originating saga/event
//   - causation_id (string, optional): Identifies the event that caused this instruction
//   - scheduled_at (string, optional): ISO 8601 timestamp for deferred dispatch
//
// Returns a map containing:
//   - instruction_id: UUID of the created instruction
//   - status: Always "PENDING" for newly queued instructions
func dispatchInstructionHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		req, err := buildDispatchRequest(ctx, params)
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := c.DispatchInstruction(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("operational_gateway.dispatch_instruction: %w", err)
		}

		instruction := resp.GetInstruction()
		return map[string]any{
			"instruction_id": instruction.GetId(),
			"status":         "PENDING",
		}, nil
	}
}

// buildDispatchRequest constructs a DispatchInstructionRequest from Starlark params.
func buildDispatchRequest(ctx *saga.StarlarkContext, params map[string]any) (*opgatewayv1.DispatchInstructionRequest, error) {
	instructionType, err := saga.RequireStringParam(params, "instruction_type")
	if err != nil {
		return nil, err
	}

	payloadStruct, err := extractPayload(params)
	if err != nil {
		return nil, err
	}

	req := &opgatewayv1.DispatchInstructionRequest{
		InstructionType: instructionType,
		Payload:         payloadStruct,
		IdempotencyKey:  &commonv1.IdempotencyKey{Key: ctx.IdempotencyKey},
	}

	if priorityStr, ok := params["priority"].(string); ok && priorityStr != "" {
		req.Priority = stringToPriority(priorityStr)
	}

	if corrID, ok := params["correlation_id"].(string); ok && corrID != "" {
		req.CorrelationId = corrID
	} else {
		req.CorrelationId = ctx.CorrelationID.String()
	}

	if causationID, ok := params["causation_id"].(string); ok && causationID != "" {
		req.CausationId = causationID
	}

	if scheduledAtStr, ok := params["scheduled_at"].(string); ok && scheduledAtStr != "" {
		t, parseErr := time.Parse(time.RFC3339, scheduledAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("operational_gateway.dispatch_instruction: invalid scheduled_at %q: %w", scheduledAtStr, parseErr)
		}
		req.ScheduledAt = timestamppb.New(t)
	}

	return req, nil
}

// extractPayload extracts and converts the payload param to a structpb.Struct.
func extractPayload(params map[string]any) (*structpb.Struct, error) {
	payloadVal, ok := params["payload"]
	if !ok || payloadVal == nil {
		return nil, fmt.Errorf("operational_gateway.dispatch_instruction: %w: payload", saga.ErrMissingParam)
	}
	payloadRaw, ok := payloadVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("operational_gateway.dispatch_instruction: %w: payload must be a map, got %T", saga.ErrInvalidParamType, payloadVal)
	}
	payloadStruct, err := structpb.NewStruct(payloadRaw)
	if err != nil {
		return nil, fmt.Errorf("operational_gateway.dispatch_instruction: failed to convert payload: %w", err)
	}
	return payloadStruct, nil
}

// cancelInstructionHandler cancels a pending instruction before dispatch.
// This is the compensation handler for dispatch_instruction.
//
// Parameters:
//   - instruction_id (string, required): UUID of the instruction to cancel
//   - reason (string, optional): Cancellation reason for audit trail
//
// Returns a map containing:
//   - instruction_id: Echo of the input instruction ID
//   - status: Always "CANCELLED"
func cancelInstructionHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		instructionID, err := saga.RequireStringParam(params, "instruction_id")
		if err != nil {
			return nil, err
		}

		req := &opgatewayv1.CancelInstructionRequest{
			InstructionId: instructionID,
		}

		if reason, ok := params["reason"].(string); ok && reason != "" {
			req.CancellationReason = reason
		}

		clientCtx := prepareClientContext(ctx)
		_, err = c.CancelInstruction(clientCtx, req)
		if err != nil {
			return nil, fmt.Errorf("operational_gateway.cancel_instruction: %w", err)
		}

		return map[string]any{
			"instruction_id": instructionID,
			"status":         "CANCELLED",
		}, nil
	}
}

// getInstructionHandler retrieves an instruction by ID.
//
// Parameters:
//   - instruction_id (string, required): UUID of the instruction to retrieve
//
// Returns a map containing:
//   - instruction_id: UUID of the instruction
//   - instruction_type: Type of instruction
//   - status: Current lifecycle status
func getInstructionHandler(c *Client) saga.Handler {
	return func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		instructionID, err := saga.RequireStringParam(params, "instruction_id")
		if err != nil {
			return nil, err
		}

		clientCtx := prepareClientContext(ctx)
		resp, err := c.GetInstruction(clientCtx, &opgatewayv1.GetInstructionRequest{
			InstructionId: instructionID,
		})
		if err != nil {
			return nil, fmt.Errorf("operational_gateway.get_instruction: %w", err)
		}

		instruction := resp.GetInstruction()
		return map[string]any{
			"instruction_id":   instruction.GetId(),
			"instruction_type": instruction.GetInstructionType(),
			"status":           instructionStatusToString(instruction.GetStatus()),
		}, nil
	}
}

// prepareClientContext enriches the gRPC client context with saga metadata.
func prepareClientContext(ctx *saga.StarlarkContext) context.Context {
	clientCtx := ctx.Context

	clientCtx = context.WithValue(clientCtx, correlationIDContextKey, ctx.CorrelationID.String())
	clientCtx = clients.PropagateIdempotencyKey(clientCtx, ctx.IdempotencyKey)
	clientCtx = clients.PropagateKnowledgeAt(clientCtx, ctx.KnowledgeAt)

	return clientCtx
}

// stringToPriority converts a Starlark priority string to the proto Priority enum.
func stringToPriority(s string) opgatewayv1.Priority {
	switch s {
	case "LOW":
		return opgatewayv1.Priority_PRIORITY_LOW
	case "NORMAL":
		return opgatewayv1.Priority_PRIORITY_NORMAL
	case "HIGH":
		return opgatewayv1.Priority_PRIORITY_HIGH
	case "CRITICAL":
		return opgatewayv1.Priority_PRIORITY_CRITICAL
	default:
		return opgatewayv1.Priority_PRIORITY_NORMAL
	}
}

// instructionStatusToString converts the proto InstructionStatus to a human-readable string.
func instructionStatusToString(s opgatewayv1.InstructionStatus) string {
	switch s {
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED:
		return "UNKNOWN"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING:
		return "PENDING"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING:
		return "DISPATCHING"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED:
		return "DELIVERED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED:
		return "ACKNOWLEDGED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING:
		return "RETRYING"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED:
		return "FAILED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED:
		return "EXPIRED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED:
		return "CANCELLED"
	}
	return "UNKNOWN"
}
