package mapping

import (
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine()
	require.NoError(t, err)
	return e
}

// --- Engine creation ---

func TestNewEngine(t *testing.T) {
	e, err := NewEngine()
	require.NoError(t, err)
	assert.NotNil(t, e)
	assert.Equal(t, 0, e.CacheLen())
}

func TestNewEngineWithCacheSize(t *testing.T) {
	e, err := NewEngineWithCacheSize(10)
	require.NoError(t, err)
	assert.NotNil(t, e)
}

func TestNewEngineWithCacheSize_Invalid(t *testing.T) {
	_, err := NewEngineWithCacheSize(0)
	require.Error(t, err)
}

// --- TransformInbound ---

func TestTransformInbound_SimpleFields(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "full_name"},
			{ExternalPath: "age", InternalPath: "user_age"},
		},
	}

	result, err := e.TransformInbound(def, []byte(`{"name":"Alice","age":30}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"full_name":"Alice"`)
	assert.Contains(t, string(result.ProtoJSON), `"user_age":30`)
}

func TestTransformInbound_InvalidJSON(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.TransformInbound(&mappingv1.MappingDefinition{}, []byte(`not json`))
	require.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformInbound_NestedPath(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "user.email", InternalPath: "email"},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"user":{"email":"a@b.com"}}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"email":"a@b.com"`)
}

func TestTransformInbound_MissingFieldSkipped(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "missing_field", InternalPath: "target"},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"other":"value"}`))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(result.ProtoJSON))
}

func TestTransformInbound_DefaultValue(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "missing",
				InternalPath: "status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DefaultValue{DefaultValue: "PENDING"},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"status":"PENDING"`)
}

func TestTransformInbound_ValidationCEL_Pass(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		InboundValidationCel: `payload.amount > 0`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"amount":100}`))
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestTransformInbound_ValidationCEL_Fail(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		InboundValidationCel: `payload.amount > 0`,
	}
	_, err := e.TransformInbound(def, []byte(`{"amount":-1}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
}

func TestTransformInbound_ValidationCEL_CompileError(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		InboundValidationCel: `invalid %%% expression`,
	}
	_, err := e.TransformInbound(def, []byte(`{}`))
	require.Error(t, err)
}

func TestTransformInbound_EnumMapping(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "internal_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"active":   "ACTIVE",
								"inactive": "INACTIVE",
							},
						},
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"status":"active"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"internal_status":"ACTIVE"`)
}

func TestTransformInbound_EnumMapping_Fallback(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "internal_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values:   map[string]string{"a": "A"},
							Fallback: "UNKNOWN",
						},
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"status":"unmapped"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"internal_status":"UNKNOWN"`)
}

func TestTransformInbound_EnumMapping_NoMapping(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "internal_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{"a": "A"},
						},
					},
				},
			},
		},
	}
	_, err := e.TransformInbound(def, []byte(`{"status":"unmapped"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnumNotMapped)
}

func TestTransformInbound_DateFormat(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "date",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"date":"15/03/2024"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), "2024-03-15T00:00:00Z")
}

func TestTransformInbound_DateFormat_Invalid(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "date",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	_, err := e.TransformInbound(def, []byte(`{"date":"not-a-date"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDateParse)
}

func TestTransformInbound_CELTransform(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "name",
				InternalPath: "upper_name",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							InboundCel: `value.upperAscii()`,
						},
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"name":"alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"upper_name":"ALICE"`)
}

func TestTransformInbound_CELTransform_EmptyExpr(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "name",
				InternalPath: "name",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{InboundCel: ""},
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"name":"alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"name":"alice"`)
}

func TestTransformInbound_ComputedField(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "first", InternalPath: "first"},
			{ExternalPath: "last", InternalPath: "last"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "full_name",
				CelExpression: `mapped.first + " " + mapped.last`,
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"first":"John","last":"Doe"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), `"full_name":"John Doe"`)
}

func TestTransformInbound_Idempotency_Selector(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "id", InternalPath: "id"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "id",
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"id":"tx-123"}`))
	require.NoError(t, err)
	assert.Equal(t, "tx-123", result.IdempotencyKey)
}

func TestTransformInbound_Idempotency_ContentHash(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "a", InternalPath: "a"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"a", "b"},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"a":"1","b":"2"}`))
	require.NoError(t, err)
	assert.NotEmpty(t, result.IdempotencyKey)
	assert.Len(t, result.IdempotencyKey, 64) // SHA256 hex
}

func TestTransformInbound_Idempotency_ContentHash_MissingField(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"missing"},
		},
	}
	_, err := e.TransformInbound(def, []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
}

func TestTransformInbound_Idempotency_ContentHash_NoFields(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash: true,
		},
	}
	_, err := e.TransformInbound(def, []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
}

func TestTransformInbound_Idempotency_SelectorMissing(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "missing",
		},
	}
	_, err := e.TransformInbound(def, []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
}

func TestTransformInbound_Idempotency_EmptySelector(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Idempotency: &mappingv1.IdempotencyConfig{},
	}
	_, err := e.TransformInbound(def, []byte(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIdempotencyKey)
}

func TestTransformInbound_NoIdempotency(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "x", InternalPath: "x"},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"x":1}`))
	require.NoError(t, err)
	assert.Empty(t, result.IdempotencyKey)
}

func TestTransformInbound_AttributeFlatten(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "",
				InternalPath: "attributes",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{
							SourceKeys: []string{"color", "size"},
						},
					},
				},
			},
		},
	}
	result, err := e.TransformInbound(def, []byte(`{"color":"red","size":"large"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result.ProtoJSON), "color")
	assert.Contains(t, string(result.ProtoJSON), "red")
}

// --- TransformOutbound ---

func TestTransformOutbound_SimpleFields(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "full_name", ExternalPath: "name"},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"full_name":"Alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"name":"Alice"`)
	// Internal path should be removed when different from external
	assert.NotContains(t, string(result), `"full_name"`)
}

func TestTransformOutbound_SamePathNotRemoved(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "name", ExternalPath: "name"},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"name":"Alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"name":"Alice"`)
}

func TestTransformOutbound_InvalidJSON(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.TransformOutbound(&mappingv1.MappingDefinition{}, []byte(`bad`))
	require.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformOutbound_EnumMapping(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "status",
				ExternalPath: "ext_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"active": "ACTIVE",
							},
						},
					},
				},
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"status":"ACTIVE"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"ext_status":"active"`)
}

func TestTransformOutbound_EnumMapping_OutboundFallback(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "status",
				ExternalPath: "ext_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values:           map[string]string{"a": "A"},
							OutboundFallback: "unknown",
						},
					},
				},
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"status":"UNMAPPED"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"ext_status":"unknown"`)
}

func TestTransformOutbound_EnumMapping_NoMapping(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "status",
				ExternalPath: "ext_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{"a": "A"},
						},
					},
				},
			},
		},
	}
	_, err := e.TransformOutbound(def, []byte(`{"status":"UNKNOWN"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnumNotMapped)
}

func TestTransformOutbound_DateFormat(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "created_at",
				ExternalPath: "date",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"created_at":"2024-03-15T00:00:00Z"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"date":"15/03/2024"`)
}

func TestTransformOutbound_DateFormat_Invalid(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "created_at",
				ExternalPath: "date",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	_, err := e.TransformOutbound(def, []byte(`{"created_at":"not-rfc3339"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDateParse)
}

func TestTransformOutbound_CELTransform(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "name",
				ExternalPath: "display",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{
							OutboundCel: `value.upperAscii()`,
						},
					},
				},
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"name":"alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"display":"ALICE"`)
}

func TestTransformOutbound_CELTransform_EmptyExpr(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "name",
				ExternalPath: "name",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_Cel{
						Cel: &mappingv1.CelTransform{OutboundCel: ""},
					},
				},
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"name":"alice"}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"name":"alice"`)
}

func TestTransformOutbound_ValidationCEL_Fail(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		OutboundValidationCel: `payload.amount > 0`,
	}
	_, err := e.TransformOutbound(def, []byte(`{"amount":-1}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidation)
}

func TestTransformOutbound_ComputedField(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "price", ExternalPath: "price"},
		},
		OutboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "formatted",
				CelExpression: `"$" + string(input.price)`,
			},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"price":42}`))
	require.NoError(t, err)
	assert.Contains(t, string(result), `"formatted":"$42"`)
}

func TestTransformOutbound_AttributeFlatten(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "attributes",
				ExternalPath: "attrs",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{},
					},
				},
			},
		},
	}
	input := `{"attributes":[{"key":"color","value":"red"},{"key":"size","value":"L"}]}`
	result, err := e.TransformOutbound(def, []byte(input))
	require.NoError(t, err)
	assert.Contains(t, string(result), "color")
	assert.Contains(t, string(result), "red")
}

func TestTransformOutbound_MissingFieldSkipped(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "nonexistent", ExternalPath: "out"},
		},
	}
	result, err := e.TransformOutbound(def, []byte(`{"other":"val"}`))
	require.NoError(t, err)
	// Field is skipped, original value preserved
	assert.Contains(t, string(result), `"other":"val"`)
}

// --- CEL cache ---

func TestCELCache(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		InboundValidationCel: `payload.x > 0`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "x", InternalPath: "x"},
		},
	}
	_, err := e.TransformInbound(def, []byte(`{"x":1}`))
	require.NoError(t, err)
	assert.True(t, e.CacheContains(`payload.x > 0`))
	assert.Equal(t, 1, e.CacheLen())

	// Second call uses cache
	_, err = e.TransformInbound(def, []byte(`{"x":2}`))
	require.NoError(t, err)
	assert.Equal(t, 1, e.CacheLen())
}

// --- EscapeJSONPath ---

func TestEscapeJSONPath(t *testing.T) {
	assert.Equal(t, `a\.b\.c`, EscapeJSONPath("a.b.c"))
	assert.Equal(t, "simple", EscapeJSONPath("simple"))
}

// --- gjsonToInterface ---

func TestGjsonToInterface_Types(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		path     string
		expected any
	}{
		{"null", `{"a":null}`, "a", nil},
		{"true", `{"a":true}`, "a", true},
		{"false", `{"a":false}`, "a", false},
		{"integer", `{"a":42}`, "a", int64(42)},
		{"float", `{"a":3.14}`, "a", 3.14},
		{"string", `{"a":"hello"}`, "a", "hello"},
		{"array", `{"a":[1,2]}`, "a", []any{int64(1), int64(2)}},
		{"object", `{"a":{"b":"c"}}`, "a", map[string]any{"b": "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Extract(tt.json, tt.path)
			val := gjsonToInterface(result)
			assert.Equal(t, tt.expected, val)
		})
	}
}
