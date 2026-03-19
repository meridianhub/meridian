package mapping

import (
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransformInboundBatch_Simple(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
		},
	}
	input := `[{"name":"Alice"},{"name":"Bob"}]`
	results, err := e.TransformInboundBatch(def, []byte(input))
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Contains(t, string(results[0].ProtoJSON), "Alice")
	assert.Contains(t, string(results[1].ProtoJSON), "Bob")
}

func TestTransformInboundBatch_InvalidJSON(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.TransformInboundBatch(&mappingv1.MappingDefinition{}, []byte(`not json`))
	require.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformInboundBatch_NotArray(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.TransformInboundBatch(&mappingv1.MappingDefinition{}, []byte(`{"key":"value"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformInboundBatch_EmptyArray(t *testing.T) {
	e := newTestEngine(t)
	results, err := e.TransformInboundBatch(&mappingv1.MappingDefinition{}, []byte(`[]`))
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestTransformInboundBatch_WithIdempotency(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "id", InternalPath: "id"},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "id",
		},
	}
	input := `[{"id":"a"},{"id":"b"}]`
	results, err := e.TransformInboundBatch(def, []byte(input))
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "a:0", results[0].IdempotencyKey)
	assert.Equal(t, "b:1", results[1].IdempotencyKey)
}

func TestTransformInboundBatch_NoIdempotency(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "x", InternalPath: "x"},
		},
	}
	results, err := e.TransformInboundBatch(def, []byte(`[{"x":1}]`))
	require.NoError(t, err)
	assert.Empty(t, results[0].IdempotencyKey)
}

func TestTransformInboundBatch_ValidationFailure(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		IsBatch:              true,
		BatchTargetPath:      "items",
		InboundValidationCel: `payload.amount > 0`,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}
	input := `[{"amount":10},{"amount":-1}]`
	_, err := e.TransformInboundBatch(def, []byte(input))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "element 1")
}

func TestTransformOutboundBatch_Simple(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		IsBatch:         true,
		BatchTargetPath: "items",
		Fields: []*mappingv1.FieldCorrespondence{
			{InternalPath: "name", ExternalPath: "display_name"},
		},
	}
	input := `{"items":[{"name":"Alice"},{"name":"Bob"}]}`
	result, err := e.TransformOutboundBatch(def, []byte(input))
	require.NoError(t, err)
	assert.Contains(t, string(result), "display_name")
	assert.Contains(t, string(result), "Alice")
}

func TestTransformOutboundBatch_InvalidJSON(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.TransformOutboundBatch(&mappingv1.MappingDefinition{}, []byte(`bad`))
	require.ErrorIs(t, err, ErrInvalidJSON)
}

func TestTransformOutboundBatch_MissingPath(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		BatchTargetPath: "items",
	}
	result, err := e.TransformOutboundBatch(def, []byte(`{"other":"val"}`))
	require.NoError(t, err)
	assert.Equal(t, "[]", string(result))
}

func TestTransformOutboundBatch_NotArray(t *testing.T) {
	e := newTestEngine(t)
	def := &mappingv1.MappingDefinition{
		BatchTargetPath: "items",
	}
	result, err := e.TransformOutboundBatch(def, []byte(`{"items":"not_array"}`))
	require.NoError(t, err)
	assert.Equal(t, "[]", string(result))
}
