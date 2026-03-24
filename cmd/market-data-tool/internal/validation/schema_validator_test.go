package validation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestNewSchemaValidator(t *testing.T) {
	t.Run("returns nil for nil proto struct", func(t *testing.T) {
		v := NewSchemaValidator(nil)
		assert.Nil(t, v)
	})

	t.Run("returns nil for nil input", func(t *testing.T) {
		assert.Nil(t, NewSchemaValidator(nil))
	})

	t.Run("creates validator from valid proto struct", func(t *testing.T) {
		s, err := structpb.NewStruct(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tenor": map[string]interface{}{"type": "string"},
			},
		})
		require.NoError(t, err)

		v := NewSchemaValidator(s)
		require.NotNil(t, v)
	})
}

func TestNewSchemaValidatorFromJSON(t *testing.T) {
	t.Run("returns nil for empty string", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON("")
		assert.Nil(t, v)
	})

	t.Run("returns nil for invalid JSON", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON("not-valid-json{{{")
		assert.Nil(t, v)
	})

	t.Run("creates validator from valid JSON schema", func(t *testing.T) {
		schema := `{"type": "object", "properties": {"tenor": {"type": "string"}}}`
		v := NewSchemaValidatorFromJSON(schema)
		require.NotNil(t, v)
	})
}

func TestSchemaValidator_Validate(t *testing.T) {
	t.Run("nil validator returns no error", func(t *testing.T) {
		var v *SchemaValidator
		err := v.Validate(map[string]string{"key": "value"})
		assert.NoError(t, err)
	})

	t.Run("passes valid attributes", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON(`{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"},
				"currency": {"type": "string"}
			},
			"required": ["tenor"]
		}`)
		require.NotNil(t, v)

		err := v.Validate(map[string]string{"tenor": "1M", "currency": "USD"})
		assert.NoError(t, err)
	})

	t.Run("rejects missing required field", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON(`{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"}
			},
			"required": ["tenor"]
		}`)
		require.NotNil(t, v)

		err := v.Validate(map[string]string{"currency": "USD"})
		require.Error(t, err)

		var schemaErr *SchemaValidationError
		var multiErr *MultiSchemaError
		isSchemaError := errors.As(err, &schemaErr) || errors.As(err, &multiErr)
		assert.True(t, isSchemaError, "expected SchemaValidationError or MultiSchemaError, got %T: %v", err, err)
	})

	t.Run("passes empty attributes when no required fields", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON(`{"type": "object"}`)
		require.NotNil(t, v)

		err := v.Validate(map[string]string{})
		assert.NoError(t, err)
	})
}

func TestSchemaValidator_ValidateWithContext(t *testing.T) {
	t.Run("nil validator returns valid result", func(t *testing.T) {
		var v *SchemaValidator
		result := v.ValidateWithContext(map[string]string{"key": "val"})
		require.NotNil(t, result)
		assert.True(t, result.Valid)
		assert.Nil(t, result.Error)
	})

	t.Run("returns valid result for passing attributes", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON(`{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"}
			},
			"required": ["tenor"]
		}`)
		require.NotNil(t, v)

		result := v.ValidateWithContext(map[string]string{"tenor": "3M"})
		assert.True(t, result.Valid)
		assert.Nil(t, result.Error)
		assert.Nil(t, result.FieldErrors)
	})

	t.Run("returns invalid result with field errors map initialized", func(t *testing.T) {
		v := NewSchemaValidatorFromJSON(`{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"}
			},
			"required": ["tenor"]
		}`)
		require.NotNil(t, v)

		result := v.ValidateWithContext(map[string]string{"other": "value"})
		assert.False(t, result.Valid)
		require.NotNil(t, result.Error)
		// FieldErrors is always initialized (non-nil) when validation fails via jsonschema
		assert.NotNil(t, result.FieldErrors)
	})
}

func TestSchemaValidationError_Error(t *testing.T) {
	t.Run("formats error with schema path", func(t *testing.T) {
		e := &SchemaValidationError{
			Path:       "/tenor",
			Message:    "missing property",
			SchemaPath: "/required/0",
		}
		msg := e.Error()
		assert.Contains(t, msg, "/tenor")
		assert.Contains(t, msg, "missing property")
		assert.Contains(t, msg, "/required/0")
	})

	t.Run("formats error without schema path", func(t *testing.T) {
		e := &SchemaValidationError{
			Path:    "/tenor",
			Message: "missing property",
		}
		msg := e.Error()
		assert.Contains(t, msg, "/tenor")
		assert.Contains(t, msg, "missing property")
		assert.NotContains(t, msg, "schema:")
	})
}

func TestMultiSchemaError_Error(t *testing.T) {
	t.Run("no errors message", func(t *testing.T) {
		e := &MultiSchemaError{}
		assert.Equal(t, "no schema validation errors", e.Error())
	})

	t.Run("single error delegates to inner error", func(t *testing.T) {
		inner := &SchemaValidationError{Path: "/tenor", Message: "missing property"}
		e := &MultiSchemaError{Errors: []*SchemaValidationError{inner}}
		assert.Equal(t, inner.Error(), e.Error())
	})

	t.Run("multiple errors combines messages", func(t *testing.T) {
		e := &MultiSchemaError{
			Errors: []*SchemaValidationError{
				{Path: "/a", Message: "err1"},
				{Path: "/b", Message: "err2"},
			},
		}
		msg := e.Error()
		assert.Contains(t, msg, "2 schema validation errors")
		assert.Contains(t, msg, "/a")
		assert.Contains(t, msg, "/b")
	})
}
