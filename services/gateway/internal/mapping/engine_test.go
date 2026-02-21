package mapping

import (
	"encoding/json"
	"strings"
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := NewEngine()
	require.NoError(t, err)
	return eng
}

// --- Simple field mapping (rename only) ---

func TestTransformInbound_SimpleFieldMapping(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "customer_name", InternalPath: "name"},
			{ExternalPath: "customer_email", InternalPath: "email"},
			{ExternalPath: "order.total", InternalPath: "amount"},
		},
	}

	input := []byte(`{"customer_name":"Alice","customer_email":"alice@example.com","order":{"total":99.95}}`)

	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	assert.Equal(t, "Alice", out["name"])
	assert.Equal(t, "alice@example.com", out["email"])
	assert.Equal(t, 99.95, out["amount"])
}

func TestTransformOutbound_SimpleFieldMapping(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "customer_name", InternalPath: "name"},
			{ExternalPath: "customer_email", InternalPath: "email"},
		},
	}

	input := []byte(`{"name":"Alice","email":"alice@example.com"}`)

	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))

	assert.Equal(t, "Alice", out["customer_name"])
	assert.Equal(t, "alice@example.com", out["customer_email"])
}

func TestTransformInbound_MissingField_Skipped(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
			{ExternalPath: "missing_field", InternalPath: "optional"},
		},
	}

	input := []byte(`{"name":"Bob"}`)

	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	assert.Equal(t, "Bob", out["name"])
	_, exists := out["optional"]
	assert.False(t, exists)
}

func TestTransformInbound_NestedPaths(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "data.customer.id", InternalPath: "customer_id"},
			{ExternalPath: "data.amount", InternalPath: "payment.amount"},
		},
	}

	input := []byte(`{"data":{"customer":{"id":"cust-123"},"amount":50.00}}`)

	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	assert.Equal(t, "cust-123", out["customer_id"])
	payment, ok := out["payment"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 50.0, payment["amount"])
}

// --- Enum mapping ---

func TestTransformInbound_EnumMapping(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "order_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"active":   "STATUS_ACTIVE",
								"inactive": "STATUS_INACTIVE",
								"pending":  "STATUS_PENDING",
							},
						},
					},
				},
			},
		},
	}

	input := []byte(`{"status":"active"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "STATUS_ACTIVE", out["order_status"])
}

func TestTransformInbound_EnumMapping_Fallback(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "order_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values:   map[string]string{"known": "KNOWN"},
							Fallback: "STATUS_UNKNOWN",
						},
					},
				},
			},
		},
	}

	input := []byte(`{"status":"unknown_value"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "STATUS_UNKNOWN", out["order_status"])
}

func TestTransformInbound_EnumMapping_NoFallback_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "order_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{"known": "KNOWN"},
						},
					},
				},
			},
		},
	}

	input := []byte(`{"status":"unmapped"}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnumNotMapped)
}

func TestTransformOutbound_EnumMapping_ReverseMap(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
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

	input := []byte(`{"order_status":"STATUS_ACTIVE"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "active", out["status"])
}

func TestTransformOutbound_EnumMapping_OutboundFallback(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "order_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values:           map[string]string{"active": "STATUS_ACTIVE"},
							OutboundFallback: "unknown",
						},
					},
				},
			},
		},
	}

	input := []byte(`{"order_status":"STATUS_NEW"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "unknown", out["status"])
}

// --- Date format ---

func TestTransformInbound_DateFormat(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "created",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "2006-01-02",
					},
				},
			},
		},
	}

	input := []byte(`{"created":"2024-03-15"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "2024-03-15T00:00:00Z", out["created_at"])
}

func TestTransformOutbound_DateFormat(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "created",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "2006-01-02",
					},
				},
			},
		},
	}

	input := []byte(`{"created_at":"2024-03-15T00:00:00Z"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "2024-03-15", out["created"])
}

func TestTransformInbound_DateFormat_InvalidDate_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "created",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "2006-01-02",
					},
				},
			},
		},
	}

	input := []byte(`{"created":"not-a-date"}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDateParse)
}

// --- CEL transform ---

func TestTransformInbound_CELTransform(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "name",
				InternalPath: "normalized_name",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel:  `value + "_processed"`,
							OutboundCel: `value.replace("_processed", "")`,
						},
					},
				},
			},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "Alice_processed", out["normalized_name"])
}

func TestTransformOutbound_CELTransform(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "name",
				InternalPath: "normalized_name",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel:  `value + "_processed"`,
							OutboundCel: `value.replace("_processed", "")`,
						},
					},
				},
			},
		},
	}

	input := []byte(`{"normalized_name":"Alice_processed"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "Alice", out["name"])
}

func TestTransformInbound_CELTransform_WithInputContext(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "amount",
				InternalPath: "total",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel: `string(value)`,
						},
					},
				},
			},
		},
	}

	input := []byte(`{"amount":42.5,"currency":"USD"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "42.5", out["total"])
}

// --- Attribute flatten ---

func TestTransformInbound_AttributeFlatten(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "color",
				InternalPath: "attributes",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{
							SourceKeys:  []string{"color", "size", "weight"},
							TargetField: "attributes",
						},
					},
				},
			},
		},
	}

	input := []byte(`{"color":"red","size":"large","weight":"5kg"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	attrs, ok := out["attributes"].([]any)
	require.True(t, ok, "attributes should be an array, got %T", out["attributes"])
	assert.Len(t, attrs, 3)

	// Verify entries contain key-value pairs.
	keys := make(map[string]string)
	for _, entry := range attrs {
		e, ok := entry.(map[string]any)
		require.True(t, ok)
		keys[e["key"].(string)] = e["value"].(string)
	}
	assert.Equal(t, "red", keys["color"])
	assert.Equal(t, "large", keys["size"])
	assert.Equal(t, "5kg", keys["weight"])
}

func TestTransformOutbound_AttributeFlatten(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "color",
				InternalPath: "attributes",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{
							SourceKeys:  []string{"color", "size"},
							TargetField: "attributes",
						},
					},
				},
			},
		},
	}

	input := []byte(`{"attributes":[{"key":"color","value":"red"},{"key":"size","value":"large"}]}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))

	// The outbound flatten produces a map of key->value.
	flatMap, ok := out["color"].(map[string]any)
	require.True(t, ok, "expected map, got %T: %v", out["color"], out["color"])
	assert.Equal(t, "red", flatMap["color"])
	assert.Equal(t, "large", flatMap["size"])
}

// --- Default value ---

func TestTransformInbound_DefaultValue(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "source",
				InternalPath: "source",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DefaultValue{
						DefaultValue: "WEBHOOK",
					},
				},
			},
		},
	}

	// Field is missing, should get default.
	input := []byte(`{"other":"data"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "WEBHOOK", out["source"])
}

func TestTransformInbound_DefaultValue_FieldExists(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "source",
				InternalPath: "source",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DefaultValue{
						DefaultValue: "WEBHOOK",
					},
				},
			},
		},
	}

	// Field exists, should use actual value.
	input := []byte(`{"source":"API"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "API", out["source"])
}

// --- Computed fields with CEL ---

func TestTransformInbound_ComputedFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "display_name",
				CelExpression: `"Hello, " + input.name`,
			},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "Alice", out["name"])
	assert.Equal(t, "Hello, Alice", out["display_name"])
}

func TestTransformInbound_ComputedFields_ChainedAccess(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "first", InternalPath: "first_name"},
			{ExternalPath: "last", InternalPath: "last_name"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "full_name",
				CelExpression: `mapped.first_name + " " + mapped.last_name`,
			},
		},
	}

	input := []byte(`{"first":"Alice","last":"Smith"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))
	assert.Equal(t, "Alice Smith", out["full_name"])
}

func TestTransformOutbound_ComputedFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
		OutboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "greeting",
				CelExpression: `"Welcome, " + input.name`,
			},
		},
	}

	input := []byte(`{"name":"Bob"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "Welcome, Bob", out["greeting"])
}

// --- Validation CEL ---

func TestTransformInbound_ValidationCEL_Pass(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		InboundValidationCel: `has(payload.customer_id) && has(payload.amount)`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "customer_id", InternalPath: "customer_id"},
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}

	input := []byte(`{"customer_id":"cust-1","amount":100}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestTransformInbound_ValidationCEL_Fail(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		InboundValidationCel: `has(payload.customer_id) && has(payload.amount)`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "customer_id", InternalPath: "customer_id"},
		},
	}

	input := []byte(`{"customer_id":"cust-1"}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
}

func TestTransformOutbound_ValidationCEL_Fail(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		OutboundValidationCel: `has(payload.status)`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	_, err := eng.TransformOutbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
}

// --- Idempotency key derivation ---

func TestTransformInbound_IdempotencyKey_SourceSelector(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "data", InternalPath: "data"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "header.idempotency_key",
		},
	}

	input := []byte(`{"header":{"idempotency_key":"abc-123"},"data":"test"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Equal(t, "abc-123", result.IdempotencyKey)
}

func TestTransformInbound_IdempotencyKey_ContentHash(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"customer_id", "amount"},
		},
	}

	input := []byte(`{"customer_id":"cust-1","amount":100}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.NotEmpty(t, result.IdempotencyKey)
	assert.Len(t, result.IdempotencyKey, 64) // SHA256 hex

	// Same input should produce the same hash.
	result2, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Equal(t, result.IdempotencyKey, result2.IdempotencyKey)

	// Different input should produce a different hash.
	input2 := []byte(`{"customer_id":"cust-2","amount":100}`)
	result3, err := eng.TransformInbound(mapping, input2)
	require.NoError(t, err)
	assert.NotEqual(t, result.IdempotencyKey, result3.IdempotencyKey)
}

func TestTransformInbound_IdempotencyKey_NoConfig(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "data", InternalPath: "data"},
		},
	}

	input := []byte(`{"data":"test"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Empty(t, result.IdempotencyKey)
}

func TestTransformInbound_IdempotencyKey_MissingSelector_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "data", InternalPath: "data"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "nonexistent.path",
		},
	}

	input := []byte(`{"data":"test"}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
}

func TestTransformInbound_IdempotencyKey_ContentHash_MissingField_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"customer_id", "amount"},
		},
	}

	// customer_id is missing from the payload.
	input := []byte(`{"amount":100}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
	assert.Contains(t, err.Error(), "customer_id")
}

// --- CEL cache ---

func TestCELCache_HitAndMiss(t *testing.T) {
	eng := newTestEngine(t)
	assert.Equal(t, 0, eng.CacheLen())

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "val",
				InternalPath: "result",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel: `value + "_suffix"`,
						},
					},
				},
			},
		},
	}

	input := []byte(`{"val":"hello"}`)

	// First call: cache miss.
	_, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Equal(t, 1, eng.CacheLen())
	assert.True(t, eng.CacheContains(`value + "_suffix"`))

	// Second call: cache hit (same program reused).
	_, err = eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Equal(t, 1, eng.CacheLen())
}

// --- Bidirectional round-trip ---

func TestRoundTrip_SimpleFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "ext_name", InternalPath: "int_name"},
			{ExternalPath: "ext_email", InternalPath: "int_email"},
		},
	}

	original := []byte(`{"ext_name":"Alice","ext_email":"alice@test.com"}`)

	// Inbound.
	inResult, err := eng.TransformInbound(mapping, original)
	require.NoError(t, err)

	var internal map[string]any
	require.NoError(t, json.Unmarshal(inResult.ProtoJSON, &internal))
	assert.Equal(t, "Alice", internal["int_name"])
	assert.Equal(t, "alice@test.com", internal["int_email"])

	// Outbound (reverse).
	outResult, err := eng.TransformOutbound(mapping, inResult.ProtoJSON)
	require.NoError(t, err)

	var external map[string]any
	require.NoError(t, json.Unmarshal(outResult, &external))
	assert.Equal(t, "Alice", external["ext_name"])
	assert.Equal(t, "alice@test.com", external["ext_email"])
}

func TestRoundTrip_EnumMapping(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
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

	original := []byte(`{"status":"active"}`)

	inResult, err := eng.TransformInbound(mapping, original)
	require.NoError(t, err)

	outResult, err := eng.TransformOutbound(mapping, inResult.ProtoJSON)
	require.NoError(t, err)

	var external map[string]any
	require.NoError(t, json.Unmarshal(outResult, &external))
	assert.Equal(t, "active", external["status"])
}

func TestRoundTrip_DateFormat(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "date",
				InternalPath: "timestamp",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "2006-01-02",
					},
				},
			},
		},
	}

	original := []byte(`{"date":"2024-06-15"}`)

	inResult, err := eng.TransformInbound(mapping, original)
	require.NoError(t, err)

	outResult, err := eng.TransformOutbound(mapping, inResult.ProtoJSON)
	require.NoError(t, err)

	var external map[string]any
	require.NoError(t, json.Unmarshal(outResult, &external))
	assert.Equal(t, "2024-06-15", external["date"])
}

func TestRoundTrip_CELTransform(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "code",
				InternalPath: "code",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel:  `"PREFIX_" + value`,
							OutboundCel: `value.replace("PREFIX_", "")`,
						},
					},
				},
			},
		},
	}

	original := []byte(`{"code":"ABC"}`)

	inResult, err := eng.TransformInbound(mapping, original)
	require.NoError(t, err)

	var internal map[string]any
	require.NoError(t, json.Unmarshal(inResult.ProtoJSON, &internal))
	assert.Equal(t, "PREFIX_ABC", internal["code"])

	outResult, err := eng.TransformOutbound(mapping, inResult.ProtoJSON)
	require.NoError(t, err)

	var external map[string]any
	require.NoError(t, json.Unmarshal(outResult, &external))
	assert.Equal(t, "ABC", external["code"])
}

// --- Invalid JSON ---

func TestTransformInbound_InvalidJSON_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
	}

	_, err := eng.TransformInbound(mapping, []byte(`{invalid`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformOutbound_InvalidJSON_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
	}

	_, err := eng.TransformOutbound(mapping, []byte(`{invalid`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

// --- Outbound passthrough ---

func TestTransformOutbound_PassthroughUnmappedFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "ext_name", InternalPath: "name"},
		},
	}

	// Proto JSON has extra fields not in the mapping.
	input := []byte(`{"name":"Alice","extra_field":"should_remain","nested":{"deep":"value"}}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))

	assert.Equal(t, "Alice", out["ext_name"])
	assert.Equal(t, "should_remain", out["extra_field"])
	// Internal field "name" should be removed since it differs from external "ext_name".
	_, namePresent := out["name"]
	assert.False(t, namePresent, "internal field 'name' should be removed from outbound output")

	nested, ok := out["nested"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value", nested["deep"])
}

func TestTransformOutbound_InternalFieldsRemovedWhenRenamed(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "customer_name", InternalPath: "name"},
			{ExternalPath: "customer_email", InternalPath: "email"},
		},
	}

	input := []byte(`{"name":"Alice","email":"alice@example.com"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))

	// External paths should be present.
	assert.Equal(t, "Alice", out["customer_name"])
	assert.Equal(t, "alice@example.com", out["customer_email"])

	// Internal paths should be removed.
	_, namePresent := out["name"]
	assert.False(t, namePresent, "internal field 'name' should not leak")
	_, emailPresent := out["email"]
	assert.False(t, emailPresent, "internal field 'email' should not leak")
}

func TestTransformOutbound_SamePaths_NotDeleted(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(output, &out))
	assert.Equal(t, "Alice", out["name"])
}

// --- Multiple transforms in same mapping ---

func TestTransformInbound_MultipleTransformTypes(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
			{
				ExternalPath: "status",
				InternalPath: "status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{"active": "ACTIVE", "inactive": "INACTIVE"},
						},
					},
				},
			},
			{
				ExternalPath: "created",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
			{
				ExternalPath: "code",
				InternalPath: "code",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel: `value + "-v2"`,
						},
					},
				},
			},
		},
	}

	input := []byte(`{"name":"Alice","status":"active","created":"15/03/2024","code":"ABC"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	assert.Equal(t, "Alice", out["name"])
	assert.Equal(t, "ACTIVE", out["status"])
	assert.Equal(t, "2024-03-15T00:00:00Z", out["created_at"])
	assert.Equal(t, "ABC-v2", out["code"])
}

// --- Boolean and numeric field types ---

func TestTransformInbound_BooleanAndNumericFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "active", InternalPath: "is_active"},
			{ExternalPath: "count", InternalPath: "item_count"},
			{ExternalPath: "price", InternalPath: "unit_price"},
		},
	}

	input := []byte(`{"active":true,"count":42,"price":19.99}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	assert.Equal(t, true, out["is_active"])
	assert.Equal(t, float64(42), out["item_count"])
	assert.Equal(t, 19.99, out["unit_price"])
}

// --- Edge cases ---

func TestTransformInbound_EmptyFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{},
	}

	input := []byte(`{"anything":"here"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(result.ProtoJSON))
}

func TestTransformOutbound_EmptyFields(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{},
	}

	input := []byte(`{"anything":"here"}`)
	output, err := eng.TransformOutbound(mapping, input)
	require.NoError(t, err)
	// Outbound passes through the original proto JSON.
	assert.Contains(t, string(output), "anything")
}

func TestTransformInbound_ArrayValues(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "tags", InternalPath: "labels"},
		},
	}

	input := []byte(`{"tags":["foo","bar","baz"]}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	labels, ok := out["labels"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"foo", "bar", "baz"}, labels)
}

// --- Engine creation ---

func TestNewEngine_Success(t *testing.T) {
	eng, err := NewEngine()
	require.NoError(t, err)
	assert.NotNil(t, eng)
}

func TestNewEngineWithCacheSize(t *testing.T) {
	eng, err := NewEngineWithCacheSize(10)
	require.NoError(t, err)
	assert.NotNil(t, eng)
}

// --- CEL compilation error ---

func TestTransformInbound_AttributeFlatten_ExternalPathMissing(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				// ExternalPath is "color" but may not exist; flatten should still run using source_keys.
				ExternalPath: "color",
				InternalPath: "attributes",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{
							SourceKeys:  []string{"size", "weight"},
							TargetField: "attributes",
						},
					},
				},
			},
		},
	}

	// "color" is absent but "size" and "weight" exist.
	input := []byte(`{"size":"large","weight":"5kg"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	attrs, ok := out["attributes"].([]any)
	require.True(t, ok, "attributes should be an array")
	assert.Len(t, attrs, 2)
}

func TestTransformInbound_CELListResult(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "tags",
				CelExpression: `["tag1", "tag2", "tag3"]`,
			},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	tags, ok := out["tags"].([]any)
	require.True(t, ok, "tags should be an array, got %T", out["tags"])
	assert.Equal(t, []any{"tag1", "tag2", "tag3"}, tags)
}

func TestTransformInbound_CELMapResult(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "metadata",
				CelExpression: `{"source": "webhook", "version": "v2"}`,
			},
		},
	}

	input := []byte(`{"name":"Alice"}`)
	result, err := eng.TransformInbound(mapping, input)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(result.ProtoJSON, &out))

	meta, ok := out["metadata"].(map[string]any)
	require.True(t, ok, "metadata should be a map, got %T", out["metadata"])
	assert.Equal(t, "webhook", meta["source"])
	assert.Equal(t, "v2", meta["version"])
}

func TestTransformInbound_InvalidCEL_Error(t *testing.T) {
	eng := newTestEngine(t)

	mapping := &mappingv1.MappingDefinition{
		InboundValidationCel: `this is not valid CEL!!!`,
	}

	input := []byte(`{"data":"test"}`)
	_, err := eng.TransformInbound(mapping, input)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "CEL"))
}
