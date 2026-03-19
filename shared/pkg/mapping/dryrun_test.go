package mapping

import (
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunInbound_Success(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "full_name"},
		},
	}
	result := e.DryRunInbound(def, []byte(`{"name":"Alice"}`))
	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)
	assert.Contains(t, result.TransformedJSON, "Alice")
	assert.GreaterOrEqual(t, result.ExecutionTimeMs, int64(0))
	require.Len(t, result.FieldTraces, 1)
	assert.Equal(t, "name", result.FieldTraces[0].SourcePath)
	assert.Equal(t, "full_name", result.FieldTraces[0].TargetPath)
	assert.Equal(t, "none", result.FieldTraces[0].TransformType)
}

func TestDryRunInbound_ValidationFails(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		InboundValidationCel: `payload.amount > 0`,
	}
	result := e.DryRunInbound(def, []byte(`{"amount":-1}`))
	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
	assert.Empty(t, result.TransformedJSON)
}

func TestDryRunInbound_TransformError(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "status",
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
	result := e.DryRunInbound(def, []byte(`{"status":"unmapped"}`))
	assert.True(t, result.ValidationPassed) // Validation passed, transform failed
	assert.NotEmpty(t, result.TransformError)
}

func TestDryRunInbound_WithIdempotency(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "id", InternalPath: "id"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "id",
		},
	}
	result := e.DryRunInbound(def, []byte(`{"id":"key-1"}`))
	assert.True(t, result.ValidationPassed)
	assert.Equal(t, "key-1", result.IdempotencyKey)
}

func TestDryRunOutbound_Success(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "full_name", ExternalPath: "name"},
		},
	}
	result := e.DryRunOutbound(def, []byte(`{"full_name":"Bob"}`))
	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)
	assert.Contains(t, result.TransformedJSON, "Bob")
	require.Len(t, result.FieldTraces, 1)
	assert.Equal(t, "full_name", result.FieldTraces[0].SourcePath)
	assert.Equal(t, "name", result.FieldTraces[0].TargetPath)
}

func TestDryRunOutbound_ValidationFails(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		OutboundValidationCel: `payload.amount > 0`,
	}
	result := e.DryRunOutbound(def, []byte(`{"amount":-1}`))
	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
}

func TestDryRunOutbound_TransformError(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				InternalPath: "date",
				ExternalPath: "date",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	result := e.DryRunOutbound(def, []byte(`{"date":"bad-date"}`))
	assert.True(t, result.ValidationPassed)
	assert.NotEmpty(t, result.TransformError)
}

func TestTransformTypeName(t *testing.T) {
	tests := []struct {
		name     string
		ft       *mappingv1.FieldTransform
		expected string
	}{
		{"nil", nil, "none"},
		{"cel", &mappingv1.FieldTransform{Transform: &mappingv1.FieldTransform_Cel{Cel: &mappingv1.CelTransform{}}}, "cel"},
		{"enum", &mappingv1.FieldTransform{Transform: &mappingv1.FieldTransform_EnumMapping{EnumMapping: &mappingv1.EnumMapping{}}}, "enum_mapping"},
		{"date", &mappingv1.FieldTransform{Transform: &mappingv1.FieldTransform_DateFormat{DateFormat: "2006"}}, "date_format"},
		{"default", &mappingv1.FieldTransform{Transform: &mappingv1.FieldTransform_DefaultValue{DefaultValue: "x"}}, "default_value"},
		{"flatten", &mappingv1.FieldTransform{Transform: &mappingv1.FieldTransform_AttributeFlatten{AttributeFlatten: &mappingv1.AttributeFlatten{}}}, "attribute_flatten"},
		{"empty", &mappingv1.FieldTransform{}, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, transformTypeName(tt.ft))
		})
	}
}

func TestRawOrEmpty(t *testing.T) {
	assert.Equal(t, "", rawOrEmpty(Extract(`{}`, "missing")))
	assert.Equal(t, `"hello"`, rawOrEmpty(Extract(`{"a":"hello"}`, "a")))
}

func TestToJSONString(t *testing.T) {
	assert.Equal(t, "null", toJSONString(nil))
	assert.Equal(t, `"hello"`, toJSONString("hello"))
	assert.Equal(t, "42", toJSONString(42))
	assert.Equal(t, "true", toJSONString(true))
}

func TestDryRunInbound_FieldTraceWithTransformError(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "date",
				InternalPath: "date",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "02/01/2006",
					},
				},
			},
		},
	}
	result := e.DryRunInbound(def, []byte(`{"date":"bad"}`))
	require.Len(t, result.FieldTraces, 1)
	assert.Contains(t, result.FieldTraces[0].TransformedValue, "<error:")
	assert.Equal(t, "date_format", result.FieldTraces[0].TransformType)
}
