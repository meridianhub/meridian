package validation

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"google.golang.org/protobuf/types/known/structpb"
)

// SchemaValidator validates observation attributes against a JSON Schema.
type SchemaValidator struct {
	schema *jsonschema.Schema
}

// NewSchemaValidator creates a new schema validator from a protobuf Struct.
// Returns nil if the schema is nil or empty.
func NewSchemaValidator(schema *structpb.Struct) *SchemaValidator {
	if schema == nil {
		return nil
	}

	// Convert protobuf Struct to JSON
	schemaJSON, err := json.Marshal(schema.AsMap())
	if err != nil {
		return nil
	}

	return NewSchemaValidatorFromJSON(string(schemaJSON))
}

// NewSchemaValidatorFromJSON creates a new schema validator from a JSON string.
// Returns nil if the schema is empty or invalid.
func NewSchemaValidatorFromJSON(schemaJSON string) *SchemaValidator {
	if schemaJSON == "" {
		return nil
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft2020

	if err := compiler.AddResource("schema.json", strings.NewReader(schemaJSON)); err != nil {
		return nil
	}

	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return nil
	}

	return &SchemaValidator{
		schema: schema,
	}
}

// Validate checks that the attributes conform to the schema.
func (v *SchemaValidator) Validate(attributes map[string]string) error {
	if v == nil || v.schema == nil {
		return nil // No schema to validate against
	}

	// Convert string map to interface map for JSON Schema validation
	data := make(map[string]interface{}, len(attributes))
	for k, val := range attributes {
		data[k] = val
	}

	if err := v.schema.Validate(data); err != nil {
		return convertSchemaError(err)
	}

	return nil
}

// convertSchemaError converts jsonschema validation errors to our error types.
func convertSchemaError(err error) error {
	var validationErr *jsonschema.ValidationError
	if !errors.As(err, &validationErr) {
		return err
	}

	// Collect all validation errors
	var errors []*SchemaValidationError

	// Walk the error tree
	walkValidationErrors(validationErr, &errors)

	if len(errors) == 0 {
		return err
	}

	if len(errors) == 1 {
		return errors[0]
	}

	return &MultiSchemaError{Errors: errors}
}

// walkValidationErrors recursively collects all validation errors.
func walkValidationErrors(err *jsonschema.ValidationError, errors *[]*SchemaValidationError) {
	if err == nil {
		return
	}

	// If this is a leaf error (has a message), add it
	if err.Message != "" {
		*errors = append(*errors, &SchemaValidationError{
			Path:       err.InstanceLocation,
			Message:    err.Message,
			SchemaPath: err.KeywordLocation,
		})
	}

	// Walk child errors
	for _, child := range err.Causes {
		walkValidationErrors(child, errors)
	}
}

// ValidateWithContext validates attributes and returns detailed error context.
func (v *SchemaValidator) ValidateWithContext(attributes map[string]string) *Result {
	result := &Result{
		Valid: true,
	}

	if v == nil || v.schema == nil {
		return result
	}

	// Convert string map to interface map for JSON Schema validation
	data := make(map[string]interface{}, len(attributes))
	for k, val := range attributes {
		data[k] = val
	}

	if err := v.schema.Validate(data); err != nil {
		result.Valid = false
		result.Error = convertSchemaError(err)

		// Extract field-specific errors
		var validationErr *jsonschema.ValidationError
		if errors.As(err, &validationErr) {
			result.FieldErrors = extractFieldErrors(validationErr)
		}
	}

	return result
}

// Result contains detailed validation results.
type Result struct {
	// Valid is true if validation passed.
	Valid bool

	// Error is the validation error (if any).
	Error error

	// FieldErrors contains errors keyed by field path.
	FieldErrors map[string]string
}

// extractFieldErrors extracts field-specific error messages.
func extractFieldErrors(err *jsonschema.ValidationError) map[string]string {
	errors := make(map[string]string)
	extractFieldErrorsRecursive(err, errors)
	return errors
}

// extractFieldErrorsRecursive recursively extracts field errors.
func extractFieldErrorsRecursive(err *jsonschema.ValidationError, errors map[string]string) {
	if err == nil {
		return
	}

	if err.Message != "" && err.InstanceLocation != "" {
		// Use the instance location as the field path
		path := err.InstanceLocation
		if path == "" {
			path = "/"
		}
		errors[path] = fmt.Sprintf("%s (at %s)", err.Message, err.KeywordLocation)
	}

	for _, child := range err.Causes {
		extractFieldErrorsRecursive(child, errors)
	}
}
