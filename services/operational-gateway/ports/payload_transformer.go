// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"
	"errors"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
)

// ErrMappingNotFound is returned when a named MappingDefinition cannot be resolved.
var ErrMappingNotFound = errors.New("mapping definition not found")

// ErrTransformFailed is returned when payload transformation fails.
var ErrTransformFailed = errors.New("payload transform failed")

// InstructionRoute defines how an instruction type is dispatched on a provider connection.
// It carries routing configuration including the HTTP method, path template, and names
// of MappingDefinitions to use for outbound and inbound transformations.
type InstructionRoute struct {
	// InstructionType is the type of instruction this route handles (e.g., "payment_order.create").
	InstructionType string

	// HTTPMethod is the HTTP verb for the outbound request (e.g., "POST", "PUT").
	HTTPMethod string

	// PathTemplate is the URL path template for the outbound request.
	// Supports simple variable substitution using {variable} syntax.
	PathTemplate string

	// OutboundMapping is the name of the MappingDefinition used to transform the instruction
	// payload (internal format) into the provider's expected request body.
	// Empty string means no mapping is applied (passthrough).
	OutboundMapping string

	// InboundMapping is the name of the MappingDefinition used to transform the provider's
	// response body into an InstructionOutcome.
	// Empty string means no mapping is applied (passthrough).
	InboundMapping string

	// Headers contains static HTTP headers to include with every outbound request on this route.
	Headers map[string]string
}

// InstructionOutcome captures the result of processing an inbound provider response.
// It represents the extracted state from a provider acknowledgement or status callback.
type InstructionOutcome struct {
	// ExternalID is the provider's identifier for the instruction, if returned.
	// May be empty if the provider does not return an external reference.
	ExternalID string

	// ProviderStatus is the provider's status string for the instruction
	// (e.g., "ACCEPTED", "PENDING", "REJECTED").
	ProviderStatus string

	// ShouldRetry indicates whether the dispatch should be retried.
	// Set to true when the provider returns a transient error (e.g., rate limit, timeout).
	ShouldRetry bool

	// FailureReason contains a human-readable description of why the instruction failed.
	// Non-empty only when the provider indicates a permanent failure.
	FailureReason string
}

// PayloadTransformer transforms instruction payloads between Meridian's internal format
// and the format expected by external providers.
//
// Outbound transformation maps the internal Instruction payload to the provider's
// request body format, together with any additional HTTP headers.
//
// Inbound transformation maps the provider's HTTP response back to an InstructionOutcome,
// extracting the provider status, external ID, and retry disposition.
type PayloadTransformer interface {
	// TransformOutbound maps an instruction's payload to the provider's expected request body.
	// Returns the serialized request body bytes and any additional HTTP headers to include.
	// Returns ErrTransformFailed if transformation cannot be completed.
	TransformOutbound(ctx context.Context, instruction *domain.Instruction, route *InstructionRoute) (body []byte, headers map[string]string, err error)

	// TransformInbound maps a provider's HTTP response to an InstructionOutcome.
	// statusCode is the HTTP response status code, body is the raw response body.
	// Returns ErrTransformFailed if transformation cannot be completed.
	TransformInbound(ctx context.Context, statusCode int, body []byte, route *InstructionRoute) (*InstructionOutcome, error)
}
