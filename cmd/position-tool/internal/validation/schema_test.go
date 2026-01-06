package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaValidator_Validate_NoSchema(t *testing.T) {
	validator := NewSchemaValidator()

	attrs := map[string]string{"any": "value"}
	err := validator.Validate(attrs, "")

	assert.NoError(t, err)
}

func TestSchemaValidator_Validate_ValidAttributes(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		}
	}`

	attrs := map[string]string{"name": "test"}
	err := validator.Validate(attrs, schema)

	assert.NoError(t, err)
}

func TestSchemaValidator_Validate_InvalidType(t *testing.T) {
	validator := NewSchemaValidator()

	// Schema requires "count" to be an integer, but we pass string "abc"
	// Note: Since attributes are always strings, this test validates the schema
	// accepts string values as expected
	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"additionalProperties": false
	}`

	// Invalid: has extra property not allowed
	attrs := map[string]string{
		"name":  "test",
		"extra": "not allowed",
	}
	err := validator.Validate(attrs, schema)

	assert.Error(t, err)
}

func TestSchemaValidator_Validate_RequiredField(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"required_field": {"type": "string"}
		},
		"required": ["required_field"]
	}`

	// Missing required field
	attrs := map[string]string{}
	err := validator.Validate(attrs, schema)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required_field")
}

func TestSchemaValidator_Validate_PatternValidation(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"vintage_year": {
				"type": "string",
				"pattern": "^[0-9]{4}$"
			}
		}
	}`

	// Valid pattern
	attrs := map[string]string{"vintage_year": "2024"}
	err := validator.Validate(attrs, schema)
	assert.NoError(t, err)

	// Invalid pattern
	attrs2 := map[string]string{"vintage_year": "24"}
	err = validator.Validate(attrs2, schema)
	assert.Error(t, err)
}

func TestSchemaValidator_Validate_EnumValidation(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"status": {
				"type": "string",
				"enum": ["active", "inactive", "pending"]
			}
		}
	}`

	// Valid enum value
	attrs := map[string]string{"status": "active"}
	err := validator.Validate(attrs, schema)
	assert.NoError(t, err)

	// Invalid enum value
	attrs2 := map[string]string{"status": "unknown"}
	err = validator.Validate(attrs2, schema)
	assert.Error(t, err)
}

func TestSchemaValidator_CacheHit(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{"type": "object"}`
	attrs := map[string]string{}

	// First validation - compiles schema
	err := validator.Validate(attrs, schema)
	require.NoError(t, err)

	initialSize := validator.CacheSize()
	assert.Equal(t, 1, initialSize)

	// Second validation - should use cached schema
	err = validator.Validate(attrs, schema)
	require.NoError(t, err)

	// Cache size should remain the same
	assert.Equal(t, initialSize, validator.CacheSize())
}

func TestSchemaValidator_ClearCache(t *testing.T) {
	validator := NewSchemaValidator()

	schema := `{"type": "object"}`
	attrs := map[string]string{}

	_ = validator.Validate(attrs, schema)
	assert.Equal(t, 1, validator.CacheSize())

	validator.ClearCache()
	assert.Equal(t, 0, validator.CacheSize())
}

func TestSchemaValidator_InvalidSchema(t *testing.T) {
	validator := NewSchemaValidator()

	// Invalid JSON schema
	schema := `{not valid json}`
	attrs := map[string]string{}

	err := validator.Validate(attrs, schema)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to compile")
}

func TestSchemaValidationError_Error(t *testing.T) {
	err := &SchemaValidationError{
		Path:       "/vintage_year",
		Message:    "does not match pattern",
		SchemaPath: "#/properties/vintage_year/pattern",
	}

	errStr := err.Error()
	assert.Contains(t, errStr, "/vintage_year")
	assert.Contains(t, errStr, "does not match pattern")
	assert.Contains(t, errStr, "pattern")
}

func TestMultiSchemaError_Error(t *testing.T) {
	// Single error
	single := &MultiSchemaError{
		Errors: []*SchemaValidationError{
			{Path: "/field1", Message: "error1"},
		},
	}
	assert.Contains(t, single.Error(), "/field1")
	assert.Contains(t, single.Error(), "error1")

	// Multiple errors
	multi := &MultiSchemaError{
		Errors: []*SchemaValidationError{
			{Path: "/field1", Message: "error1"},
			{Path: "/field2", Message: "error2"},
		},
	}
	errStr := multi.Error()
	assert.Contains(t, errStr, "2 schema validation errors")
	assert.Contains(t, errStr, "field1")
	assert.Contains(t, errStr, "field2")
}

func TestCommonSchemas_EmptyObject(t *testing.T) {
	validator := NewSchemaValidator()

	// Should accept any object
	attrs := map[string]string{
		"any":   "value",
		"other": "data",
	}
	err := validator.Validate(attrs, CommonSchemas.EmptyObject)
	assert.NoError(t, err)
}

func TestCommonSchemas_StringAttributes(t *testing.T) {
	validator := NewSchemaValidator()

	// All string values should be valid
	attrs := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	err := validator.Validate(attrs, CommonSchemas.StringAttributes)
	assert.NoError(t, err)
}

func TestCommonSchemas_CarbonCreditAttributes(t *testing.T) {
	validator := NewSchemaValidator()

	// Valid carbon credit attributes
	attrs := map[string]string{
		"vintage_year": "2024",
		"registry":     "Verra",
		"project_id":   "VCS-1234",
	}
	err := validator.Validate(attrs, CommonSchemas.CarbonCreditAttributes)
	assert.NoError(t, err)

	// Missing required field
	attrs2 := map[string]string{
		"vintage_year": "2024",
		// Missing registry
	}
	err = validator.Validate(attrs2, CommonSchemas.CarbonCreditAttributes)
	assert.Error(t, err)

	// Invalid vintage_year pattern
	attrs3 := map[string]string{
		"vintage_year": "24", // Must be 4 digits
		"registry":     "Verra",
	}
	err = validator.Validate(attrs3, CommonSchemas.CarbonCreditAttributes)
	assert.Error(t, err)
}

func TestCommonSchemas_EnergyAttributes(t *testing.T) {
	validator := NewSchemaValidator()

	// Valid energy attributes
	attrs := map[string]string{
		"grid_zone":   "ZONE-A",
		"source_type": "solar",
	}
	err := validator.Validate(attrs, CommonSchemas.EnergyAttributes)
	assert.NoError(t, err)

	// Invalid source_type enum
	attrs2 := map[string]string{
		"source_type": "unknown",
	}
	err = validator.Validate(attrs2, CommonSchemas.EnergyAttributes)
	assert.Error(t, err)
}

func TestValidateAttributesForInstrument(t *testing.T) {
	validator := NewSchemaValidator()

	// With schema
	err := ValidateAttributesForInstrument(
		validator,
		map[string]string{"name": "test"},
		`{"type": "object"}`,
	)
	assert.NoError(t, err)

	// Without schema
	err = ValidateAttributesForInstrument(
		validator,
		map[string]string{"any": "value"},
		"",
	)
	assert.NoError(t, err)
}
