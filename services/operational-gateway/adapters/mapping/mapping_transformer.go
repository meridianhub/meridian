// Package mapping provides the MappingTransformer adapter for the operational gateway.
// It delegates payload transformation to the shared bidirectional mapping engine, which
// applies MappingDefinition proto transforms between the internal instruction format
// and the provider's external format.
package mapping

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"
)

// DefinitionResolver looks up a MappingDefinition by name for the current tenant.
// Implementations may call the reference-data gRPC service, query a local cache,
// or read from the manifest.
type DefinitionResolver interface {
	// Resolve returns the latest active MappingDefinition with the given name.
	// Returns ports.ErrMappingNotFound if no active definition exists.
	Resolve(ctx context.Context, name string) (*mappingv1.MappingDefinition, error)
}

// Transformer applies bidirectional MappingDefinition transforms to instruction payloads.
// It implements ports.PayloadTransformer using the shared mapping engine.
type Transformer struct {
	resolver DefinitionResolver
	engine   *sharedmapping.Engine
	logger   *slog.Logger
}

// NewTransformer creates a new mapping Transformer.
func NewTransformer(resolver DefinitionResolver, engine *sharedmapping.Engine, logger *slog.Logger) *Transformer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Transformer{
		resolver: resolver,
		engine:   engine,
		logger:   logger,
	}
}

// TransformOutbound serializes the instruction payload to JSON and applies the outbound
// MappingDefinition transform named by route.OutboundMapping.
// When route.OutboundMapping is empty, the instruction payload is returned as-is (passthrough).
// Returns the transformed body bytes and any static route headers.
func (t *Transformer) TransformOutbound(ctx context.Context, instruction *domain.Instruction, route *ports.InstructionRoute) ([]byte, map[string]string, error) {
	payloadBytes, err := json.Marshal(instruction.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: marshaling instruction payload: %w", ports.ErrTransformFailed, err)
	}

	if route.OutboundMapping == "" {
		return payloadBytes, copyHeaders(route.Headers), nil
	}

	def, err := t.resolver.Resolve(ctx, route.OutboundMapping)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: resolving outbound mapping %q: %w", ports.ErrTransformFailed, route.OutboundMapping, err)
	}

	transformed, err := t.engine.TransformOutbound(def, payloadBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: applying outbound mapping %q: %w", ports.ErrTransformFailed, route.OutboundMapping, err)
	}

	t.logger.DebugContext(ctx, "outbound transform applied",
		"mapping", route.OutboundMapping,
		"instruction_id", instruction.ID.String(),
	)

	return transformed, copyHeaders(route.Headers), nil
}

// TransformInbound applies the inbound MappingDefinition transform named by route.InboundMapping
// to the provider response body and extracts an InstructionOutcome.
// When route.InboundMapping is empty, a default InstructionOutcome is derived from the HTTP
// status code: 2xx is treated as success with no external ID, anything else as a failure.
func (t *Transformer) TransformInbound(ctx context.Context, statusCode int, body []byte, route *ports.InstructionRoute) (*ports.InstructionOutcome, error) {
	if route.InboundMapping == "" {
		return defaultOutcome(statusCode), nil
	}

	def, err := t.resolver.Resolve(ctx, route.InboundMapping)
	if err != nil {
		return nil, fmt.Errorf("%w: resolving inbound mapping %q: %w", ports.ErrTransformFailed, route.InboundMapping, err)
	}

	result, err := t.engine.TransformInbound(def, body)
	if err != nil {
		return nil, fmt.Errorf("%w: applying inbound mapping %q: %w", ports.ErrTransformFailed, route.InboundMapping, err)
	}

	t.logger.DebugContext(ctx, "inbound transform applied",
		"mapping", route.InboundMapping,
		"status_code", statusCode,
	)

	outcome, err := extractOutcome(result.ProtoJSON, statusCode)
	if err != nil {
		return nil, fmt.Errorf("%w: extracting outcome from inbound mapping result: %w", ports.ErrTransformFailed, err)
	}

	return outcome, nil
}

// extractOutcome parses the mapped JSON output into an InstructionOutcome.
// It looks for the well-known fields: external_id, provider_status, should_retry, failure_reason.
// Unknown fields in the mapped output are silently ignored.
func extractOutcome(mappedJSON []byte, statusCode int) (*ports.InstructionOutcome, error) {
	var mapped struct {
		ExternalID     string `json:"external_id"`
		ProviderStatus string `json:"provider_status"`
		ShouldRetry    bool   `json:"should_retry"`
		FailureReason  string `json:"failure_reason"`
	}

	if len(mappedJSON) > 0 {
		if err := json.Unmarshal(mappedJSON, &mapped); err != nil {
			return nil, fmt.Errorf("parsing mapped outcome JSON: %w", err)
		}
	}

	outcome := &ports.InstructionOutcome{
		ExternalID:     mapped.ExternalID,
		ProviderStatus: mapped.ProviderStatus,
		ShouldRetry:    mapped.ShouldRetry,
		FailureReason:  mapped.FailureReason,
	}

	// If provider_status is absent from the mapping, fall back to HTTP status code semantics.
	if outcome.ProviderStatus == "" {
		if isSuccess(statusCode) {
			outcome.ProviderStatus = "ACCEPTED"
		} else {
			outcome.ProviderStatus = "REJECTED"
			if outcome.FailureReason == "" {
				outcome.FailureReason = fmt.Sprintf("provider returned HTTP %d", statusCode)
			}
		}
	}

	return outcome, nil
}

// defaultOutcome derives an InstructionOutcome from the HTTP status code alone,
// used when no inbound mapping is configured for a route.
func defaultOutcome(statusCode int) *ports.InstructionOutcome {
	if isSuccess(statusCode) {
		return &ports.InstructionOutcome{
			ProviderStatus: "ACCEPTED",
		}
	}
	return &ports.InstructionOutcome{
		ProviderStatus: "REJECTED",
		FailureReason:  fmt.Sprintf("provider returned HTTP %d", statusCode),
	}
}

// isSuccess returns true if the HTTP status code indicates a successful response (2xx).
func isSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

// copyHeaders returns a shallow copy of the header map, or nil if the map is empty.
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
