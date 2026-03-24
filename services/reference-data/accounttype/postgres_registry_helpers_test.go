package accounttype

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// hasNonEmptySchema
// ---------------------------------------------------------------------------

func TestHasNonEmptySchema_NilOrEmpty(t *testing.T) {
	assert.False(t, hasNonEmptySchema(nil))
	assert.False(t, hasNonEmptySchema(json.RawMessage("")))
	assert.False(t, hasNonEmptySchema(json.RawMessage("  ")))
}

func TestHasNonEmptySchema_EmptyObject(t *testing.T) {
	assert.False(t, hasNonEmptySchema(json.RawMessage("{}")))
	assert.False(t, hasNonEmptySchema(json.RawMessage("  {}  ")))
}

func TestHasNonEmptySchema_NullLiteral(t *testing.T) {
	assert.False(t, hasNonEmptySchema(json.RawMessage("null")))
}

func TestHasNonEmptySchema_NonEmptySchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}}}`)
	assert.True(t, hasNonEmptySchema(schema))
}

// ---------------------------------------------------------------------------
// nullString
// ---------------------------------------------------------------------------

func TestNullString_EmptyString(t *testing.T) {
	ns := nullString("")
	assert.False(t, ns.Valid)
	assert.Equal(t, "", ns.String)
}

func TestNullString_NonEmptyString(t *testing.T) {
	ns := nullString("hello")
	assert.True(t, ns.Valid)
	assert.Equal(t, "hello", ns.String)
}

func TestNullString_Whitespace(t *testing.T) {
	// Whitespace is NOT empty - only empty string becomes NULL.
	ns := nullString("  ")
	assert.True(t, ns.Valid)
	assert.Equal(t, "  ", ns.String)
}

// ---------------------------------------------------------------------------
// marshalAttributes
// ---------------------------------------------------------------------------

func TestMarshalAttributes_Nil(t *testing.T) {
	b, err := marshalAttributes(nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("{}"), b)
}

func TestMarshalAttributes_EmptyMap(t *testing.T) {
	b, err := marshalAttributes(map[string]any{})
	require.NoError(t, err)
	assert.JSONEq(t, "{}", string(b))
}

func TestMarshalAttributes_NonEmpty(t *testing.T) {
	attrs := map[string]any{"key": "value", "count": 42}
	b, err := marshalAttributes(attrs)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(b, &result))
	assert.Equal(t, "value", result["key"])
	assert.Equal(t, float64(42), result["count"])
}

// ---------------------------------------------------------------------------
// validateJSONSchema
// ---------------------------------------------------------------------------

func TestValidateJSONSchema_ValidMinimalSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	err := validateJSONSchema(schema)
	assert.NoError(t, err)
}

func TestValidateJSONSchema_ValidFullSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"amount": {"type": "number"},
			"currency": {"type": "string"}
		},
		"required": ["amount"]
	}`)
	err := validateJSONSchema(schema)
	assert.NoError(t, err)
}

func TestValidateJSONSchema_InvalidJSON(t *testing.T) {
	schema := json.RawMessage(`not valid json`)
	err := validateJSONSchema(schema)
	assert.Error(t, err)
}

func TestValidateJSONSchema_InvalidSchemaStructure(t *testing.T) {
	// Valid JSON but invalid JSON Schema (unknown type).
	schema := json.RawMessage(`{"type": "not_a_valid_type"}`)
	err := validateJSONSchema(schema)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// validateSchemaIfPresent
// ---------------------------------------------------------------------------

func TestValidateSchemaIfPresent_NilSchema(t *testing.T) {
	err := validateSchemaIfPresent(nil)
	assert.NoError(t, err)
}

func TestValidateSchemaIfPresent_EmptyObjectSchema(t *testing.T) {
	err := validateSchemaIfPresent(json.RawMessage("{}"))
	assert.NoError(t, err)
}

func TestValidateSchemaIfPresent_ValidSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}}}`)
	err := validateSchemaIfPresent(schema)
	assert.NoError(t, err)
}

func TestValidateSchemaIfPresent_InvalidSchemaWrapsError(t *testing.T) {
	schema := json.RawMessage(`not json`)
	err := validateSchemaIfPresent(schema)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidAttributeSchema)
}

// ---------------------------------------------------------------------------
// validateAttributesAgainstSchema
// ---------------------------------------------------------------------------

func TestValidateAttributesAgainstSchema_ValidAttributes(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"tier": {"type": "string"},
			"limit": {"type": "number"}
		},
		"required": ["tier"]
	}`)
	attrs := map[string]any{"tier": "gold", "limit": 5000.0}
	err := validateAttributesAgainstSchema(schema, attrs)
	assert.NoError(t, err)
}

func TestValidateAttributesAgainstSchema_MissingRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"tier": {"type": "string"}},
		"required": ["tier"]
	}`)
	attrs := map[string]any{"limit": 1000.0}
	err := validateAttributesAgainstSchema(schema, attrs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributesInvalid)
}

func TestValidateAttributesAgainstSchema_WrongType(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"limit": {"type": "number"}}
	}`)
	attrs := map[string]any{"limit": "not-a-number"}
	err := validateAttributesAgainstSchema(schema, attrs)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributesInvalid)
}

func TestValidateAttributesAgainstSchema_EmptyAttrsEmptySchema(t *testing.T) {
	schema := json.RawMessage(`{"type": "object"}`)
	attrs := map[string]any{}
	err := validateAttributesAgainstSchema(schema, attrs)
	assert.NoError(t, err)
}
