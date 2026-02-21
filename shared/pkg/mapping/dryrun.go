package mapping

import (
	"encoding/json"
	"fmt"
	"time"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/tidwall/gjson"
)

// FieldTrace records the outcome of a single field transform during a dry run.
type FieldTrace struct {
	SourcePath       string
	TargetPath       string
	SourceValue      string
	TransformedValue string
	TransformType    string
}

// DryRunResult holds all output from a dry-run execution.
// ValidationPassed reflects only whether the CEL validation expression passed.
// If validation passes but a transform error occurs, ValidationPassed remains true,
// TransformError holds the error message, and TransformedJSON is empty.
// Field-level errors appear in FieldTraces for per-field diagnostics.
type DryRunResult struct {
	TransformedJSON  string
	IdempotencyKey   string
	ValidationPassed bool
	ValidationErrors []string
	TransformError   string
	ExecutionTimeMs  int64
	FieldTraces      []FieldTrace
}

// DryRunInbound executes a dry run for the inbound (external → internal) direction.
// It captures per-field traces while performing the same transformation the live engine would.
func (e *Engine) DryRunInbound(def *mappingv1.MappingDefinition, sampleJSON []byte) *DryRunResult {
	start := time.Now()
	result := &DryRunResult{}

	// Validation: report failure and stop if CEL validation rejects the payload.
	if err := e.runValidationCEL(def.GetInboundValidationCel(), sampleJSON); err != nil {
		result.ValidationPassed = false
		result.ValidationErrors = []string{err.Error()}
		result.ExecutionTimeMs = time.Since(start).Milliseconds()
		return result
	}
	result.ValidationPassed = true

	// Collect field traces before the full transform so errors are visible per-field.
	result.FieldTraces = e.traceInboundFields(def.GetFields(), sampleJSON)

	// Run the actual transform. If it fails, ValidationPassed stays true (validation did pass);
	// TransformError captures the failure and TransformedJSON stays empty.
	inbound, err := e.TransformInbound(def, sampleJSON)
	if err != nil {
		result.TransformError = err.Error()
	} else {
		result.TransformedJSON = string(inbound.ProtoJSON)
		result.IdempotencyKey = inbound.IdempotencyKey
	}

	result.ExecutionTimeMs = time.Since(start).Milliseconds()
	return result
}

// DryRunOutbound executes a dry run for the outbound (internal → external) direction.
func (e *Engine) DryRunOutbound(def *mappingv1.MappingDefinition, sampleJSON []byte) *DryRunResult {
	start := time.Now()
	result := &DryRunResult{}

	// Validation: report failure and stop if CEL validation rejects the payload.
	if err := e.runValidationCEL(def.GetOutboundValidationCel(), sampleJSON); err != nil {
		result.ValidationPassed = false
		result.ValidationErrors = []string{err.Error()}
		result.ExecutionTimeMs = time.Since(start).Milliseconds()
		return result
	}
	result.ValidationPassed = true

	// Collect field traces before the full transform so errors are visible per-field.
	result.FieldTraces = e.traceOutboundFields(def.GetFields(), sampleJSON)

	// Run the actual transform. If it fails, ValidationPassed stays true (validation did pass);
	// TransformError captures the failure and TransformedJSON stays empty.
	outJSON, err := e.TransformOutbound(def, sampleJSON)
	if err != nil {
		result.TransformError = err.Error()
	} else {
		result.TransformedJSON = string(outJSON)
	}

	result.ExecutionTimeMs = time.Since(start).Milliseconds()
	return result
}

// traceInboundFields captures per-field trace information for inbound direction.
// Errors during individual field transforms are captured in the trace rather than aborting.
func (e *Engine) traceInboundFields(fields []*mappingv1.FieldCorrespondence, externalJSON []byte) []FieldTrace {
	traces := make([]FieldTrace, 0, len(fields))
	for _, field := range fields {
		trace := FieldTrace{
			SourcePath:    field.GetExternalPath(),
			TargetPath:    field.GetInternalPath(),
			TransformType: transformTypeName(field.GetTransform()),
		}

		val := gjson.GetBytes(externalJSON, field.GetExternalPath())
		trace.SourceValue = rawOrEmpty(val)

		transformed, err := e.applyInboundTransform(field.GetTransform(), val, externalJSON)
		if err != nil {
			trace.TransformedValue = fmt.Sprintf("<error: %v>", err)
		} else {
			trace.TransformedValue = toJSONString(transformed)
		}

		traces = append(traces, trace)
	}
	return traces
}

// traceOutboundFields captures per-field trace information for outbound direction.
func (e *Engine) traceOutboundFields(fields []*mappingv1.FieldCorrespondence, protoJSON []byte) []FieldTrace {
	traces := make([]FieldTrace, 0, len(fields))
	for _, field := range fields {
		trace := FieldTrace{
			SourcePath:    field.GetInternalPath(),
			TargetPath:    field.GetExternalPath(),
			TransformType: transformTypeName(field.GetTransform()),
		}

		val := gjson.GetBytes(protoJSON, field.GetInternalPath())
		trace.SourceValue = rawOrEmpty(val)

		transformed, err := e.applyOutboundTransform(field.GetTransform(), val, protoJSON)
		if err != nil {
			trace.TransformedValue = fmt.Sprintf("<error: %v>", err)
		} else {
			trace.TransformedValue = toJSONString(transformed)
		}

		traces = append(traces, trace)
	}
	return traces
}

// transformTypeName returns the human-readable name of the transform type.
func transformTypeName(ft *mappingv1.FieldTransform) string {
	if ft == nil {
		return "none"
	}
	switch {
	case ft.GetCel() != nil:
		return "cel"
	case ft.GetEnumMapping() != nil:
		return "enum_mapping"
	case ft.GetDateFormat() != "":
		return "date_format"
	case ft.GetDefaultValue() != "":
		return "default_value"
	case ft.GetAttributeFlatten() != nil:
		return "attribute_flatten"
	default:
		return "none"
	}
}

// rawOrEmpty returns the raw JSON representation of a gjson.Result, or empty string if not found.
func rawOrEmpty(r gjson.Result) string {
	if !r.Exists() {
		return ""
	}
	return r.Raw
}

// toJSONString converts any Go value to its JSON string representation.
func toJSONString(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
