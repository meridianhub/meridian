package mapping_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/mapping"
)

// ---------------------------------------------------------------------------
// MappingStatus.CanTransitionTo
// ---------------------------------------------------------------------------

func TestMappingStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name   string
		from   mapping.Status
		to     mapping.Status
		expect bool
	}{
		{"DRAFT to ACTIVE is allowed", mapping.StatusDraft, mapping.StatusActive, true},
		{"DRAFT to DEPRECATED is forbidden", mapping.StatusDraft, mapping.StatusDeprecated, false},
		{"DRAFT to DRAFT is forbidden", mapping.StatusDraft, mapping.StatusDraft, false},
		{"ACTIVE to DEPRECATED is allowed", mapping.StatusActive, mapping.StatusDeprecated, true},
		{"ACTIVE to DRAFT is forbidden", mapping.StatusActive, mapping.StatusDraft, false},
		{"ACTIVE to ACTIVE is forbidden", mapping.StatusActive, mapping.StatusActive, false},
		{"DEPRECATED to DRAFT is forbidden", mapping.StatusDeprecated, mapping.StatusDraft, false},
		{"DEPRECATED to ACTIVE is forbidden", mapping.StatusDeprecated, mapping.StatusActive, false},
		{"DEPRECATED to DEPRECATED is forbidden", mapping.StatusDeprecated, mapping.StatusDeprecated, false},
		{"unknown status to ACTIVE is forbidden", mapping.Status("UNKNOWN"), mapping.StatusActive, false},
		{"ACTIVE to unknown status is forbidden", mapping.StatusActive, mapping.Status("UNKNOWN"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, tt.from.CanTransitionTo(tt.to))
		})
	}
}

// ---------------------------------------------------------------------------
// MarshalComputedFields / UnmarshalComputedFields
// ---------------------------------------------------------------------------

func TestMarshalComputedFields_NilInput(t *testing.T) {
	data, err := mapping.MarshalComputedFields(nil)
	require.NoError(t, err)
	assert.JSONEq(t, "[]", string(data))
}

func TestMarshalComputedFields_EmptySlice(t *testing.T) {
	data, err := mapping.MarshalComputedFields([]mapping.ComputedField{})
	require.NoError(t, err)
	assert.JSONEq(t, "[]", string(data))
}

func TestMarshalComputedFields_Populated(t *testing.T) {
	fields := []mapping.ComputedField{
		{TargetPath: "created_at", CELExpression: "now()"},
		{TargetPath: "status", CELExpression: "'ACTIVE'"},
	}
	data, err := mapping.MarshalComputedFields(fields)
	require.NoError(t, err)

	var roundtrip []mapping.ComputedField
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	assert.Len(t, roundtrip, 2)
	assert.Equal(t, "created_at", roundtrip[0].TargetPath)
}

func TestUnmarshalComputedFields_NilInput(t *testing.T) {
	fields, err := mapping.UnmarshalComputedFields(nil)
	require.NoError(t, err)
	assert.Nil(t, fields)
}

func TestUnmarshalComputedFields_EmptyInput(t *testing.T) {
	fields, err := mapping.UnmarshalComputedFields([]byte{})
	require.NoError(t, err)
	assert.Nil(t, fields)
}

func TestUnmarshalComputedFields_Populated(t *testing.T) {
	input := `[{"target_path":"foo","cel_expression":"bar"}]`
	fields, err := mapping.UnmarshalComputedFields([]byte(input))
	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "foo", fields[0].TargetPath)
	assert.Equal(t, "bar", fields[0].CELExpression)
}

func TestUnmarshalComputedFields_InvalidJSON(t *testing.T) {
	_, err := mapping.UnmarshalComputedFields([]byte(`{not json`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// MarshalIdempotency / UnmarshalIdempotency
// ---------------------------------------------------------------------------

func TestMarshalIdempotency_NilInput(t *testing.T) {
	data, err := mapping.MarshalIdempotency(nil)
	require.NoError(t, err)
	assert.Equal(t, "null", string(data))
}

func TestMarshalIdempotency_Populated(t *testing.T) {
	cfg := &mapping.IdempotencyConfig{
		SourceSelector:    "header.key",
		UseContentHash:    true,
		ContentHashFields: []string{"amount", "reference"},
	}
	data, err := mapping.MarshalIdempotency(cfg)
	require.NoError(t, err)

	var roundtrip mapping.IdempotencyConfig
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	assert.Equal(t, "header.key", roundtrip.SourceSelector)
	assert.True(t, roundtrip.UseContentHash)
	assert.Equal(t, []string{"amount", "reference"}, roundtrip.ContentHashFields)
}

func TestUnmarshalIdempotency_NilInput(t *testing.T) {
	cfg, err := mapping.UnmarshalIdempotency(nil)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestUnmarshalIdempotency_EmptyInput(t *testing.T) {
	cfg, err := mapping.UnmarshalIdempotency([]byte{})
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestUnmarshalIdempotency_NullLiteral(t *testing.T) {
	cfg, err := mapping.UnmarshalIdempotency([]byte("null"))
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestUnmarshalIdempotency_NullLiteralWithWhitespace(t *testing.T) {
	cfg, err := mapping.UnmarshalIdempotency([]byte("  null  "))
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestUnmarshalIdempotency_Populated(t *testing.T) {
	input := `{"source_selector":"ref","use_content_hash":false}`
	cfg, err := mapping.UnmarshalIdempotency([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "ref", cfg.SourceSelector)
	assert.False(t, cfg.UseContentHash)
}

func TestUnmarshalIdempotency_InvalidJSON(t *testing.T) {
	_, err := mapping.UnmarshalIdempotency([]byte(`{broken`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// MarshalFields / UnmarshalFields round-trip
// ---------------------------------------------------------------------------

func TestMarshalFields_NilInput(t *testing.T) {
	data, err := mapping.MarshalFields(nil)
	require.NoError(t, err)
	assert.JSONEq(t, "[]", string(data))
}

func TestMarshalFields_EmptySlice(t *testing.T) {
	data, err := mapping.MarshalFields([]mapping.FieldCorrespondence{})
	require.NoError(t, err)
	assert.JSONEq(t, "[]", string(data))
}

func TestMarshalFields_Populated(t *testing.T) {
	fields := []mapping.FieldCorrespondence{
		{ExternalPath: "ext", InternalPath: "int"},
	}
	data, err := mapping.MarshalFields(fields)
	require.NoError(t, err)

	roundtrip, err := mapping.UnmarshalFields(data)
	require.NoError(t, err)
	require.Len(t, roundtrip, 1)
	assert.Equal(t, "ext", roundtrip[0].ExternalPath)
}

func TestUnmarshalFields_NilInput(t *testing.T) {
	fields, err := mapping.UnmarshalFields(nil)
	require.NoError(t, err)
	assert.Nil(t, fields)
}

func TestUnmarshalFields_EmptyInput(t *testing.T) {
	fields, err := mapping.UnmarshalFields([]byte{})
	require.NoError(t, err)
	assert.Nil(t, fields)
}

func TestUnmarshalFields_InvalidJSON(t *testing.T) {
	_, err := mapping.UnmarshalFields([]byte(`not-json`))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Marshal/Unmarshal idempotency (round-trip stability)
// ---------------------------------------------------------------------------

func TestMarshalIdempotency_RoundTrip_Nil(t *testing.T) {
	data, err := mapping.MarshalIdempotency(nil)
	require.NoError(t, err)

	cfg, err := mapping.UnmarshalIdempotency(data)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestMarshalIdempotency_RoundTrip_Empty(t *testing.T) {
	cfg := &mapping.IdempotencyConfig{}
	data, err := mapping.MarshalIdempotency(cfg)
	require.NoError(t, err)

	result, err := mapping.UnmarshalIdempotency(data)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "", result.SourceSelector)
	assert.False(t, result.UseContentHash)
}

func TestMarshalIdempotency_RoundTrip_Populated(t *testing.T) {
	cfg := &mapping.IdempotencyConfig{
		SourceSelector:    "x.y",
		UseContentHash:    true,
		ContentHashFields: []string{"a", "b"},
	}
	data, err := mapping.MarshalIdempotency(cfg)
	require.NoError(t, err)

	result, err := mapping.UnmarshalIdempotency(data)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, cfg.SourceSelector, result.SourceSelector)
	assert.Equal(t, cfg.UseContentHash, result.UseContentHash)
	assert.Equal(t, cfg.ContentHashFields, result.ContentHashFields)
}

func TestMarshalComputedFields_RoundTrip_Nil(t *testing.T) {
	data, err := mapping.MarshalComputedFields(nil)
	require.NoError(t, err)

	result, err := mapping.UnmarshalComputedFields(data)
	require.NoError(t, err)
	// "[]" unmarshals to an empty slice, not nil
	assert.Empty(t, result)
}

func TestMarshalComputedFields_RoundTrip_Populated(t *testing.T) {
	fields := []mapping.ComputedField{
		{TargetPath: "a", CELExpression: "b"},
	}
	data, err := mapping.MarshalComputedFields(fields)
	require.NoError(t, err)

	result, err := mapping.UnmarshalComputedFields(data)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, fields[0], result[0])
}
