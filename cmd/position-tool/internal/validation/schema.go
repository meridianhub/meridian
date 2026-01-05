package validation

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// SchemaValidator validates attribute maps against JSON Schema definitions.
// It caches compiled schemas for performance.
//
// Thread-safety: All methods are safe for concurrent use.
type SchemaValidator struct {
	// compiler creates JSON Schema validators
	compiler *jsonschema.Compiler

	// schemas caches compiled schemas keyed by instrument code
	schemas map[string]*jsonschema.Schema
	mu      sync.RWMutex
}

// NewSchemaValidator creates a new schema validator.
func NewSchemaValidator() *SchemaValidator {
	return &SchemaValidator{
		compiler: jsonschema.NewCompiler(),
		schemas:  make(map[string]*jsonschema.Schema),
	}
}

// Validate checks if attributes conform to the given JSON Schema.
// Returns nil if valid, or a MultiSchemaError containing all validation errors.
func (sv *SchemaValidator) Validate(attributes map[string]string, schemaJSON string) error {
	if schemaJSON == "" {
		// No schema defined - all attributes are valid
		return nil
	}

	schema, err := sv.getOrCompileSchema(schemaJSON)
	if err != nil {
		return fmt.Errorf("failed to compile JSON Schema: %w", err)
	}

	// Convert attributes to interface{} for validation
	attrsMap := make(map[string]interface{})
	for k, v := range attributes {
		attrsMap[k] = v
	}

	// Validate against schema
	if err := schema.Validate(attrsMap); err != nil {
		return sv.convertValidationError(err)
	}

	return nil
}

// ValidateJSON validates a JSON object against the given schema.
func (sv *SchemaValidator) ValidateJSON(jsonData []byte, schemaJSON string) error {
	if schemaJSON == "" {
		return nil
	}

	schema, err := sv.getOrCompileSchema(schemaJSON)
	if err != nil {
		return fmt.Errorf("failed to compile JSON Schema: %w", err)
	}

	var data interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	if err := schema.Validate(data); err != nil {
		return sv.convertValidationError(err)
	}

	return nil
}

// getOrCompileSchema retrieves a cached schema or compiles a new one.
func (sv *SchemaValidator) getOrCompileSchema(schemaJSON string) (*jsonschema.Schema, error) {
	// Create a cache key from the schema JSON
	cacheKey := schemaJSON

	// Check cache first
	sv.mu.RLock()
	if schema, ok := sv.schemas[cacheKey]; ok {
		sv.mu.RUnlock()
		return schema, nil
	}
	sv.mu.RUnlock()

	// Compile the schema
	sv.mu.Lock()
	defer sv.mu.Unlock()

	// Double-check after acquiring write lock
	if schema, ok := sv.schemas[cacheKey]; ok {
		return schema, nil
	}

	// Use a unique URI for this schema
	schemaURI := fmt.Sprintf("mem://schema/%d", len(sv.schemas))

	if err := sv.compiler.AddResource(schemaURI, strings.NewReader(schemaJSON)); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := sv.compiler.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	sv.schemas[cacheKey] = schema
	return schema, nil
}

// convertValidationError converts jsonschema errors to our error types.
func (sv *SchemaValidator) convertValidationError(err error) error {
	validationErr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return &SchemaValidationError{
			Path:    "/",
			Message: err.Error(),
		}
	}

	errors := sv.extractValidationErrors(validationErr)
	if len(errors) == 1 {
		return errors[0]
	}
	return &MultiSchemaError{Errors: errors}
}

// extractValidationErrors recursively extracts all validation errors.
func (sv *SchemaValidator) extractValidationErrors(err *jsonschema.ValidationError) []*SchemaValidationError {
	var errors []*SchemaValidationError

	// Add this error if it has a message
	if err.Message != "" {
		errors = append(errors, &SchemaValidationError{
			Path:       err.InstanceLocation,
			Message:    err.Message,
			SchemaPath: err.KeywordLocation,
		})
	}

	// Process child errors
	for _, cause := range err.Causes {
		errors = append(errors, sv.extractValidationErrors(cause)...)
	}

	return errors
}

// ClearCache removes all cached schemas.
func (sv *SchemaValidator) ClearCache() {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	sv.compiler = jsonschema.NewCompiler()
	sv.schemas = make(map[string]*jsonschema.Schema)
}

// CacheSize returns the number of cached schemas.
func (sv *SchemaValidator) CacheSize() int {
	sv.mu.RLock()
	defer sv.mu.RUnlock()
	return len(sv.schemas)
}

// ValidateAttributesForInstrument validates attributes against an instrument's schema.
// This is a convenience method that combines schema lookup and validation.
func ValidateAttributesForInstrument(
	validator *SchemaValidator,
	attributes map[string]string,
	attributeSchema string,
) error {
	if attributeSchema == "" {
		return nil
	}

	return validator.Validate(attributes, attributeSchema)
}

// CommonSchemas provides pre-defined schemas for common attribute patterns.
var CommonSchemas = struct {
	// EmptyObject allows any object (no constraints).
	EmptyObject string

	// StringAttributes requires all values to be strings.
	StringAttributes string

	// EnergyAttributes validates common energy trading attributes.
	EnergyAttributes string

	// CarbonCreditAttributes validates carbon credit attributes.
	CarbonCreditAttributes string
}{
	EmptyObject: `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object"
	}`,

	StringAttributes: `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"additionalProperties": {
			"type": "string"
		}
	}`,

	EnergyAttributes: `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"grid_zone": {
				"type": "string",
				"description": "Electrical grid zone identifier"
			},
			"source_type": {
				"type": "string",
				"enum": ["solar", "wind", "hydro", "nuclear", "gas", "coal", "mixed"],
				"description": "Generation source type"
			},
			"settlement_period": {
				"type": "string",
				"pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$",
				"description": "Settlement period timestamp (RFC3339)"
			}
		},
		"additionalProperties": {
			"type": "string"
		}
	}`,

	CarbonCreditAttributes: `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"properties": {
			"vintage_year": {
				"type": "string",
				"pattern": "^[0-9]{4}$",
				"description": "Year the carbon credit was issued"
			},
			"registry": {
				"type": "string",
				"description": "Carbon credit registry (e.g., Verra, Gold Standard)"
			},
			"project_id": {
				"type": "string",
				"description": "Unique project identifier in the registry"
			},
			"project_type": {
				"type": "string",
				"description": "Type of carbon offset project"
			}
		},
		"required": ["vintage_year", "registry"],
		"additionalProperties": {
			"type": "string"
		}
	}`,
}
