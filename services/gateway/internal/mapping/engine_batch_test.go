package mapping

import (
	"encoding/json"
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// batchMapping returns a MappingDefinition with is_batch=true and the given batch_target_path.
func batchMapping(batchTargetPath string, fields []*mappingv1.FieldCorrespondence) *mappingv1.MappingDefinition {
	return &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: batchTargetPath,
		Fields:          fields,
	}
}

// --- Inbound batch ---

func TestTransformInbound_Batch_MultiElement(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "full_name"},
		{ExternalPath: "amount", InternalPath: "total"},
	})

	input := []byte(`[{"name":"Alice","amount":100},{"name":"Bob","amount":200}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Element 0
	var out0 map[string]any
	require.NoError(t, json.Unmarshal(result[0].ProtoJSON, &out0))
	items0 := out0["items"].([]any)
	require.Len(t, items0, 1)
	elem0 := items0[0].(map[string]any)
	assert.Equal(t, "Alice", elem0["full_name"])
	assert.Equal(t, float64(100), elem0["total"])

	// Element 1
	var out1 map[string]any
	require.NoError(t, json.Unmarshal(result[1].ProtoJSON, &out1))
	items1 := out1["items"].([]any)
	require.Len(t, items1, 1)
	elem1 := items1[0].(map[string]any)
	assert.Equal(t, "Bob", elem1["full_name"])
	assert.Equal(t, float64(200), elem1["total"])
}

func TestTransformInbound_Batch_SingleElement(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("events", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "id", InternalPath: "event_id"},
	})

	input := []byte(`[{"id":"evt-1"}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 1)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result[0].ProtoJSON, &out))
	events := out["events"].([]any)
	require.Len(t, events, 1)
	assert.Equal(t, "evt-1", events[0].(map[string]any)["event_id"])
}

func TestTransformInbound_Batch_EmptyArray(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	input := []byte(`[]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestTransformInbound_Batch_NotAnArray_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	// Input is an object, not an array.
	input := []byte(`{"name":"Alice"}`)

	_, err := eng.TransformInboundBatch(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformInbound_Batch_InvalidJSON_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	_, err := eng.TransformInboundBatch(mapping, []byte(`[invalid`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformInbound_Batch_ElementError_IncludesIndex(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{
			ExternalPath: "status",
			InternalPath: "status",
			Transform: &mappingv1.FieldTransform{
				Transform: &mappingv1.FieldTransform_EnumMapping{
					EnumMapping: &mappingv1.EnumMapping{
						Values: map[string]string{"active": "ACTIVE"},
						// No fallback - unmapped value causes error.
					},
				},
			},
		},
	})

	// Second element has an unmapped status value.
	input := []byte(`[{"status":"active"},{"status":"unknown"}]`)

	_, err := eng.TransformInboundBatch(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnumNotMapped)
	assert.Contains(t, err.Error(), "element 1")
}

// --- Inbound batch idempotency ---

func TestTransformInbound_Batch_IdempotencyKey_SourceSelector(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "ref", InternalPath: "ref"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "ref",
		},
	}

	input := []byte(`[{"ref":"ref-001"},{"ref":"ref-002"}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Keys should be base_key + index suffix.
	assert.Equal(t, "ref-001:0", result[0].IdempotencyKey)
	assert.Equal(t, "ref-002:1", result[1].IdempotencyKey)
}

func TestTransformInbound_Batch_IdempotencyKey_ContentHash(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"amount"},
		},
	}

	input := []byte(`[{"amount":100},{"amount":200}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Content hash keys should differ (different amounts) and each include index suffix.
	assert.NotEmpty(t, result[0].IdempotencyKey)
	assert.NotEmpty(t, result[1].IdempotencyKey)
	assert.NotEqual(t, result[0].IdempotencyKey, result[1].IdempotencyKey)
	assert.Contains(t, result[0].IdempotencyKey, ":0")
	assert.Contains(t, result[1].IdempotencyKey, ":1")
}

func TestTransformInbound_Batch_IdempotencyKey_NoConfig(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	input := []byte(`[{"name":"Alice"}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0].IdempotencyKey)
}

// --- Outbound batch ---

func TestTransformOutbound_Batch_MultiElement(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "full_name"},
		{ExternalPath: "amount", InternalPath: "total"},
	})

	// Proto JSON with items array at batch_target_path.
	protoJSON := []byte(`{"items":[{"full_name":"Alice","total":100},{"full_name":"Bob","total":200}]}`)

	result, err := eng.TransformOutboundBatch(mapping, protoJSON)
	require.NoError(t, err)

	// Should return a plain JSON array.
	var arr []any
	require.NoError(t, json.Unmarshal(result, &arr))
	require.Len(t, arr, 2)

	elem0 := arr[0].(map[string]any)
	assert.Equal(t, "Alice", elem0["name"])
	assert.Equal(t, float64(100), elem0["amount"])

	elem1 := arr[1].(map[string]any)
	assert.Equal(t, "Bob", elem1["name"])
	assert.Equal(t, float64(200), elem1["amount"])
}

func TestTransformOutbound_Batch_SingleElement(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("events", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "id", InternalPath: "event_id"},
	})

	protoJSON := []byte(`{"events":[{"event_id":"evt-1"}]}`)

	result, err := eng.TransformOutboundBatch(mapping, protoJSON)
	require.NoError(t, err)

	var arr []any
	require.NoError(t, json.Unmarshal(result, &arr))
	require.Len(t, arr, 1)
	assert.Equal(t, "evt-1", arr[0].(map[string]any)["id"])
}

func TestTransformOutbound_Batch_EmptyArray(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	protoJSON := []byte(`{"items":[]}`)

	result, err := eng.TransformOutboundBatch(mapping, protoJSON)
	require.NoError(t, err)

	var arr []any
	require.NoError(t, json.Unmarshal(result, &arr))
	assert.Empty(t, arr)
}

func TestTransformOutbound_Batch_InvalidJSON_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	_, err := eng.TransformOutboundBatch(mapping, []byte(`{invalid`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformOutbound_Batch_MissingTargetPath_ReturnsEmpty(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "name", InternalPath: "name"},
	})

	// Proto JSON does not contain "items".
	protoJSON := []byte(`{"other":"data"}`)

	result, err := eng.TransformOutboundBatch(mapping, protoJSON)
	require.NoError(t, err)

	var arr []any
	require.NoError(t, json.Unmarshal(result, &arr))
	assert.Empty(t, arr)
}

// --- Batch round-trip ---

func TestRoundTrip_Batch_EnumMapping(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "orders",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "id", InternalPath: "order_id"},
			{
				ExternalPath: "status",
				InternalPath: "order_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"active":   "STATUS_ACTIVE",
								"inactive": "STATUS_INACTIVE",
							},
						},
					},
				},
			},
		},
	}

	original := []byte(`[{"id":"o1","status":"active"},{"id":"o2","status":"inactive"}]`)

	// Inbound: each element gets its own InboundResult with the element wrapped at batch_target_path.
	inResults, err := eng.TransformInboundBatch(mapping, original)
	require.NoError(t, err)
	require.Len(t, inResults, 2)

	// Outbound: rebuild array from individual protos, then call TransformOutboundBatch.
	// In practice the caller would pass the assembled proto JSON; here we build one from the first result.
	var out0 map[string]any
	require.NoError(t, json.Unmarshal(inResults[0].ProtoJSON, &out0))
	orders0 := out0["orders"].([]any)
	assert.Equal(t, "o1", orders0[0].(map[string]any)["order_id"])
	assert.Equal(t, "STATUS_ACTIVE", orders0[0].(map[string]any)["order_status"])

	// Outbound reverse: build a proto with both elements.
	combinedProto := []byte(`{"orders":[{"order_id":"o1","order_status":"STATUS_ACTIVE"},{"order_id":"o2","order_status":"STATUS_INACTIVE"}]}`)
	outResult, err := eng.TransformOutboundBatch(mapping, combinedProto)
	require.NoError(t, err)

	var arr []any
	require.NoError(t, json.Unmarshal(outResult, &arr))
	require.Len(t, arr, 2)
	assert.Equal(t, "o1", arr[0].(map[string]any)["id"])
	assert.Equal(t, "active", arr[0].(map[string]any)["status"])
	assert.Equal(t, "o2", arr[1].(map[string]any)["id"])
	assert.Equal(t, "inactive", arr[1].(map[string]any)["status"])
}

// --- Per-element validation CEL ---

func TestTransformInbound_Batch_ValidationCEL_PerElement_Pass(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:              true,
		BatchTargetPath:      "items",
		InboundValidationCel: `has(payload.amount) && payload.amount > 0`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}

	input := []byte(`[{"amount":10},{"amount":20}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestTransformInbound_Batch_ValidationCEL_PerElement_Fail(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:              true,
		BatchTargetPath:      "items",
		InboundValidationCel: `has(payload.amount) && payload.amount > 0`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}

	// Second element has amount=0, which fails validation.
	input := []byte(`[{"amount":10},{"amount":0}]`)

	_, err := eng.TransformInboundBatch(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
	assert.Contains(t, err.Error(), "element 1")
}

func TestTransformOutbound_Batch_ValidationCEL_PerElement_Fail(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		IsBatch:               true,
		BatchTargetPath:       "items",
		OutboundValidationCel: `has(payload.amount)`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}

	// Second element missing "amount".
	protoJSON := []byte(`{"items":[{"amount":10},{"name":"no-amount"}]}`)

	_, err := eng.TransformOutboundBatch(mapping, protoJSON)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
	assert.Contains(t, err.Error(), "element 1")
}

// --- Nested batch_target_path ---

func TestTransformInbound_Batch_NestedTargetPath(t *testing.T) {
	eng := newTestEngine(t)

	mapping := batchMapping("data.items", []*mappingv1.FieldCorrespondence{
		{ExternalPath: "id", InternalPath: "item_id"},
	})

	input := []byte(`[{"id":"a"},{"id":"b"}]`)

	result, err := eng.TransformInboundBatch(mapping, input)
	require.NoError(t, err)
	require.Len(t, result, 2)

	var out0 map[string]any
	require.NoError(t, json.Unmarshal(result[0].ProtoJSON, &out0))

	data, ok := out0["data"].(map[string]any)
	require.True(t, ok, "expected data to be an object, got %T", out0["data"])
	items, ok := data["items"].([]any)
	require.True(t, ok, "expected items to be an array, got %T", data["items"])
	require.Len(t, items, 1)
	assert.Equal(t, "a", items[0].(map[string]any)["item_id"])
}
