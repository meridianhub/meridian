// Package tools provides formatting helpers and enum string converters for gateway tools.
package tools

import (
	"strings"
	"time"

	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
)

// --- Formatting helpers ---

// formatInstructionSummary formats an Instruction for list display.
func formatInstructionSummary(instr *opgatewayv1.Instruction) map[string]interface{} {
	if instr == nil {
		return nil
	}
	entry := map[string]interface{}{
		"id":                     instr.Id,
		"instruction_type":       instr.InstructionType,
		"provider_connection_id": instr.ProviderConnectionId,
		"status":                 instructionStatusString(instr.Status),
		"priority":               priorityString(instr.Priority),
		"attempt_count":          len(instr.Attempts),
	}
	if instr.CorrelationId != "" {
		entry["correlation_id"] = instr.CorrelationId
	}
	if instr.CausationId != "" {
		entry["causation_id"] = instr.CausationId
	}
	if instr.ScheduledAt != nil {
		entry["scheduled_at"] = instr.ScheduledAt.AsTime().Format(time.RFC3339)
	}
	if instr.ExpiresAt != nil {
		entry["expires_at"] = instr.ExpiresAt.AsTime().Format(time.RFC3339)
	}
	if instr.CreatedAt != nil {
		entry["created_at"] = instr.CreatedAt.AsTime().Format(time.RFC3339)
	}
	if instr.UpdatedAt != nil {
		entry["updated_at"] = instr.UpdatedAt.AsTime().Format(time.RFC3339)
	}
	return entry
}

// formatInstructionDetail formats an Instruction with full attempt history.
func formatInstructionDetail(instr *opgatewayv1.Instruction) map[string]interface{} {
	if instr == nil {
		return nil
	}
	entry := formatInstructionSummary(instr)

	if len(instr.Metadata) > 0 {
		entry["metadata"] = instr.Metadata
	}

	attempts := make([]map[string]interface{}, 0, len(instr.Attempts))
	for _, a := range instr.Attempts {
		attempt := map[string]interface{}{
			"attempt_number":       a.AttemptNumber,
			"response_status_code": a.ResponseStatusCode,
			"duration_ms":          a.DurationMs,
		}
		if a.DispatchedAt != nil {
			attempt["dispatched_at"] = a.DispatchedAt.AsTime().Format(time.RFC3339)
		}
		if a.ResponseBodyPreview != "" {
			attempt["response_body_preview"] = a.ResponseBodyPreview
		}
		if a.ErrorMessage != "" {
			attempt["error_message"] = a.ErrorMessage
		}
		attempts = append(attempts, attempt)
	}
	entry["attempts"] = attempts

	return entry
}

// formatConnectionHealth formats a ProviderConnection for health display.
func formatConnectionHealth(conn *opgatewayv1.ProviderConnection) map[string]interface{} {
	if conn == nil {
		return nil
	}
	entry := map[string]interface{}{
		"connection_id": conn.ConnectionId,
		"provider_name": conn.ProviderName,
		"provider_type": conn.ProviderType,
		"protocol":      protocolString(conn.Protocol),
		"base_url":      conn.BaseUrl,
		"health_status": healthStatusString(conn.HealthStatus),
		"auth_method":   authMethodName(conn),
	}
	if conn.LastHealthCheckAt != nil {
		entry["last_health_check_at"] = conn.LastHealthCheckAt.AsTime().Format(time.RFC3339)
	}
	if conn.RetryPolicy != nil {
		entry["retry_policy"] = map[string]interface{}{
			"max_attempts":            conn.RetryPolicy.MaxAttempts,
			"initial_backoff_seconds": conn.RetryPolicy.InitialBackoffSeconds,
			"max_backoff_seconds":     conn.RetryPolicy.MaxBackoffSeconds,
			"backoff_multiplier":      conn.RetryPolicy.BackoffMultiplier,
		}
	}
	if conn.RateLimit != nil {
		entry["rate_limit"] = map[string]interface{}{
			"requests_per_second": conn.RateLimit.RequestsPerSecond,
			"burst_size":          conn.RateLimit.BurstSize,
		}
	}
	if conn.CreatedAt != nil {
		entry["created_at"] = conn.CreatedAt.AsTime().Format(time.RFC3339)
	}
	if conn.UpdatedAt != nil {
		entry["updated_at"] = conn.UpdatedAt.AsTime().Format(time.RFC3339)
	}
	return entry
}

// authMethodName returns a human-readable auth method name without exposing secrets.
func authMethodName(conn *opgatewayv1.ProviderConnection) string {
	switch conn.AuthConfig.(type) {
	case *opgatewayv1.ProviderConnection_ApiKey:
		return "api_key"
	case *opgatewayv1.ProviderConnection_Basic:
		return "basic"
	case *opgatewayv1.ProviderConnection_Oauth2:
		return "oauth2"
	case *opgatewayv1.ProviderConnection_Hmac:
		return "hmac"
	case *opgatewayv1.ProviderConnection_Mtls:
		return "mtls"
	default:
		return "unknown"
	}
}

// statusUnspecified is the string representation of an unspecified/unknown enum value.
const statusUnspecified = "UNSPECIFIED"

// Instruction status string constants shared across gateway tools.
const (
	statusPending   = "PENDING"
	statusFailed    = "FAILED"
	statusCancelled = "CANCELLED"
)

// --- Enum string converters ---

// instructionStatusFromString maps a status string to the proto enum value.
func instructionStatusFromString(s string) opgatewayv1.InstructionStatus {
	switch strings.ToUpper(s) {
	case statusPending:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING
	case "DISPATCHING":
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING
	case "DELIVERED":
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED
	case "ACKNOWLEDGED":
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED
	case "RETRYING":
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING
	case statusFailed:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED
	case "EXPIRED":
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED
	case statusCancelled:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED
	default:
		return opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED
	}
}

// instructionStatusString converts a proto InstructionStatus to a clean string.
func instructionStatusString(s opgatewayv1.InstructionStatus) string {
	switch s {
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_UNSPECIFIED:
		return statusUnspecified
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_PENDING:
		return statusPending
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DISPATCHING:
		return "DISPATCHING"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_DELIVERED:
		return "DELIVERED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_ACKNOWLEDGED:
		return "ACKNOWLEDGED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_RETRYING:
		return "RETRYING"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_FAILED:
		return statusFailed
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_EXPIRED:
		return "EXPIRED"
	case opgatewayv1.InstructionStatus_INSTRUCTION_STATUS_CANCELLED:
		return statusCancelled
	}
	return statusUnspecified
}

// priorityString converts a proto Priority to a clean string.
func priorityString(p opgatewayv1.Priority) string {
	switch p {
	case opgatewayv1.Priority_PRIORITY_UNSPECIFIED:
		return statusUnspecified
	case opgatewayv1.Priority_PRIORITY_LOW:
		return "LOW"
	case opgatewayv1.Priority_PRIORITY_NORMAL:
		return "NORMAL"
	case opgatewayv1.Priority_PRIORITY_HIGH:
		return "HIGH"
	case opgatewayv1.Priority_PRIORITY_CRITICAL:
		return "CRITICAL"
	}
	return statusUnspecified
}

// healthStatusFromString maps a health status string to the proto enum value.
func healthStatusFromString(s string) opgatewayv1.HealthStatus {
	switch strings.ToUpper(s) {
	case "HEALTHY":
		return opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY
	case "DEGRADED":
		return opgatewayv1.HealthStatus_HEALTH_STATUS_DEGRADED
	case "UNHEALTHY":
		return opgatewayv1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	default:
		return opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED
	}
}

// healthStatusString converts a proto HealthStatus to a clean string.
func healthStatusString(s opgatewayv1.HealthStatus) string {
	switch s {
	case opgatewayv1.HealthStatus_HEALTH_STATUS_UNSPECIFIED:
		return statusUnspecified
	case opgatewayv1.HealthStatus_HEALTH_STATUS_HEALTHY:
		return "HEALTHY"
	case opgatewayv1.HealthStatus_HEALTH_STATUS_DEGRADED:
		return "DEGRADED"
	case opgatewayv1.HealthStatus_HEALTH_STATUS_UNHEALTHY:
		return "UNHEALTHY"
	}
	return statusUnspecified
}

// protocolString converts a proto Protocol to a clean string.
func protocolString(p opgatewayv1.Protocol) string {
	switch p {
	case opgatewayv1.Protocol_PROTOCOL_UNSPECIFIED:
		return statusUnspecified
	case opgatewayv1.Protocol_PROTOCOL_HTTPS:
		return "HTTPS"
	case opgatewayv1.Protocol_PROTOCOL_GRPC:
		return "GRPC"
	case opgatewayv1.Protocol_PROTOCOL_WEBHOOK:
		return "WEBHOOK"
	case opgatewayv1.Protocol_PROTOCOL_MQTT:
		return "MQTT"
	case opgatewayv1.Protocol_PROTOCOL_AMQP:
		return "AMQP"
	}
	return statusUnspecified
}
