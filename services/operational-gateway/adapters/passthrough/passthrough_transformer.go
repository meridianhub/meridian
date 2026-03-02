// Package passthrough provides a no-op PayloadTransformer that returns the instruction
// payload as-is without any field mapping. It is used when no mapping configuration is
// available for a provider connection, or as a fallback during development and testing.
package passthrough

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// Transformer is a PayloadTransformer that performs no field transformation.
// TransformOutbound serializes the instruction payload to JSON and returns it unchanged.
// TransformInbound derives an InstructionOutcome from the HTTP status code alone.
type Transformer struct{}

// NewTransformer creates a new passthrough Transformer.
func NewTransformer() *Transformer {
	return &Transformer{}
}

// TransformOutbound serializes the instruction payload to JSON without modification.
// Returns static headers from the route and the raw JSON payload.
func (t *Transformer) TransformOutbound(_ context.Context, instruction *domain.Instruction, route *ports.InstructionRoute) ([]byte, map[string]string, error) {
	if instruction == nil {
		return nil, nil, fmt.Errorf("%w: instruction is nil", ports.ErrTransformFailed)
	}
	if route == nil {
		return nil, nil, fmt.Errorf("%w: route is nil", ports.ErrTransformFailed)
	}
	body, err := json.Marshal(instruction.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: marshaling instruction payload: %w", ports.ErrTransformFailed, err)
	}

	headers := copyHeaders(route.Headers)
	return body, headers, nil
}

// TransformInbound derives an InstructionOutcome from the HTTP status code.
// HTTP 2xx responses map to ProviderStatus "ACCEPTED"; all other codes map to "REJECTED"
// with a failure reason containing the status code.
func (t *Transformer) TransformInbound(_ context.Context, statusCode int, _ []byte, _ *ports.InstructionRoute) (*ports.InstructionOutcome, error) {
	if statusCode >= 200 && statusCode < 300 {
		return &ports.InstructionOutcome{
			ProviderStatus: "ACCEPTED",
		}, nil
	}
	return &ports.InstructionOutcome{
		ProviderStatus: "REJECTED",
		FailureReason:  fmt.Sprintf("provider returned HTTP %d", statusCode),
	}, nil
}

// copyHeaders returns a shallow copy of the header map, or nil if empty.
func copyHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}
