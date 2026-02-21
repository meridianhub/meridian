package mapping

import (
	"fmt"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// TransformInboundBatch handles batch inbound transformation when mapping.IsBatch is true.
//
// externalJSON must be a JSON array. Each element is transformed individually using the
// existing per-element mapping logic. The transformed element is wrapped at
// mapping.BatchTargetPath in its own proto JSON envelope.
//
// Idempotency keys (when configured) are derived per element: the base key is computed
// from each element, then the element index is appended as ":<index>".
//
// On per-element failure, the error wraps the element index for diagnostics.
func (e *Engine) TransformInboundBatch(mapping *mappingv1.MappingDefinition, externalJSON []byte) ([]*InboundResult, error) {
	if !gjson.ValidBytes(externalJSON) {
		return nil, ErrInvalidJSON
	}

	parsed := gjson.ParseBytes(externalJSON)
	if !parsed.IsArray() {
		return nil, fmt.Errorf("%w: batch mode requires a JSON array as input", ErrInvalidJSON)
	}

	var elements []gjson.Result
	parsed.ForEach(func(_, v gjson.Result) bool {
		elements = append(elements, v)
		return true
	})

	if len(elements) == 0 {
		return nil, nil
	}

	results := make([]*InboundResult, 0, len(elements))
	for i, elem := range elements {
		elemJSON := []byte(elem.Raw)

		transformed, err := e.applyInboundFields(mapping.GetFields(), elemJSON)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		transformed, err = e.applyComputedFields(mapping.GetInboundComputedFields(), elemJSON, transformed)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		// Wrap the single transformed element as a one-element array at batch_target_path.
		var elemInterface any
		if gjson.Valid(transformed) {
			elemInterface = gjsonToInterface(gjson.Parse(transformed))
		}

		protoOutput := "{}"
		protoOutput, err = sjson.Set(protoOutput, mapping.GetBatchTargetPath(), []any{elemInterface})
		if err != nil {
			return nil, fmt.Errorf("%w: element %d: wrapping at %s: %w", ErrFieldExtraction, i, mapping.GetBatchTargetPath(), err)
		}

		idempotencyKey, err := e.deriveElementIdempotencyKey(mapping.GetIdempotency(), elemJSON, i)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		results = append(results, &InboundResult{
			ProtoJSON:      []byte(protoOutput),
			IdempotencyKey: idempotencyKey,
		})
	}

	return results, nil
}

// TransformOutboundBatch handles batch outbound transformation when mapping.IsBatch is true.
//
// protoJSON must be a JSON object containing an array at mapping.BatchTargetPath.
// Each element in the array is transformed individually using the reverse mapping logic.
// The result is a plain JSON array.
func (e *Engine) TransformOutboundBatch(mapping *mappingv1.MappingDefinition, protoJSON []byte) ([]byte, error) {
	if !gjson.ValidBytes(protoJSON) {
		return nil, ErrInvalidJSON
	}

	arrayVal := gjson.GetBytes(protoJSON, mapping.GetBatchTargetPath())
	if !arrayVal.Exists() || !arrayVal.IsArray() {
		// Return empty array when path is absent or not an array.
		return []byte("[]"), nil
	}

	var elements []gjson.Result
	arrayVal.ForEach(func(_, v gjson.Result) bool {
		elements = append(elements, v)
		return true
	})

	results := make([]any, 0, len(elements))
	for i, elem := range elements {
		elemJSON := []byte(elem.Raw)

		transformed, err := e.applyOutboundFields(mapping.GetFields(), elemJSON)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		transformed, err = e.applyComputedFields(mapping.GetOutboundComputedFields(), elemJSON, transformed)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}

		results = append(results, gjsonToInterface(gjson.Parse(transformed)))
	}

	output, err := sjson.Set("", "arr", results)
	if err != nil {
		return nil, fmt.Errorf("%w: marshaling batch output: %w", ErrTransform, err)
	}
	// sjson wraps in an object; extract just the array.
	arr := gjson.Get(output, "arr")
	if !arr.Exists() {
		return []byte("[]"), nil
	}
	return []byte(arr.Raw), nil
}

// deriveElementIdempotencyKey derives the idempotency key for a single batch element.
// The base key is computed from the element JSON, then the element index is appended as ":<index>".
// If no idempotency config is set, returns an empty string.
func (e *Engine) deriveElementIdempotencyKey(config *mappingv1.IdempotencyConfig, elemJSON []byte, index int) (string, error) {
	if config == nil {
		return "", nil
	}

	baseKey, err := e.deriveIdempotencyKey(config, elemJSON)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%d", baseKey, index), nil
}
