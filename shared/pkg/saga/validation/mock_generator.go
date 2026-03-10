// Package validation provides mock handler generation from handler schemas for dry-run validation.
package validation

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/shopspring/decimal"
)

// GenerateMockHandler creates a deterministic mock handler from a handler definition.
// The mock returns schema-compliant test data based on the handler's return types.
//
// Mock generation rules:
// - string fields: "mock_<field_name>"
// - Decimal fields: "100.00"
// - int64 fields: 1000
// - enum fields: first valid value from schema
// - array fields: empty array []
// - map fields: empty map {} or echo input if field matches param
// - Fields matching input params: echo the input value
//
// Mocks are deterministic - same input parameters produce same output.
func GenerateMockHandler(handlerDef *schema.HandlerDef) saga.Handler {
	return func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		result := make(map[string]any)

		// Generate mock values for each return field
		for fieldName, fieldDef := range handlerDef.Returns {
			result[fieldName] = generateMockValue(fieldName, fieldDef, params)
		}

		return result, nil
	}
}

// generateMockValue generates a deterministic mock value for a single field.
// If the field name matches an input parameter, the parameter value is echoed.
// Otherwise, a type-appropriate mock value is generated.
func generateMockValue(fieldName string, fieldDef *schema.FieldDef, params map[string]any) any {
	// Check if this field should echo an input parameter
	if echoValue, found := params[fieldName]; found {
		return echoValue
	}

	// Handle account_id alias (special case for position_keeping handlers)
	if fieldName == "position_id" {
		if accountID, found := params["account_id"]; found {
			return accountID
		}
	}

	// Generate type-specific mock value
	return generateByType(fieldName, fieldDef)
}

// generateByType generates a mock value based on the field type.
func generateByType(fieldName string, fieldDef *schema.FieldDef) any {
	switch fieldDef.Type {
	case schema.TypeString:
		return generateStringValue(fieldName, fieldDef)
	case schema.TypeDecimal:
		return decimal.NewFromFloat(100.00)
	case schema.TypeInt32:
		return int32(1000)
	case schema.TypeInt64:
		return int64(1000)
	case schema.TypeUint32:
		return uint32(1000)
	case schema.TypeBool:
		return true
	case schema.TypeEnum:
		return generateEnumValue(fieldDef)
	case schema.TypeArray:
		return []any{}
	case schema.TypeMap:
		return map[string]any{}
	case schema.TypeUUID:
		return uuid.MustParse("00000000-0000-0000-0000-000000000001")
	default:
		return nil
	}
}

// generateStringValue generates a mock string value, with special handling for status fields.
func generateStringValue(fieldName string, fieldDef *schema.FieldDef) string {
	// Special handling for status fields - extract from description
	if fieldName == "status" && fieldDef.Description != "" {
		if statusValue := extractStatusFromDescription(fieldDef.Description); statusValue != "" {
			return statusValue
		}
	}
	// String fields use "mock_<field_name>" pattern
	return fmt.Sprintf("mock_%s", fieldName)
}

// generateEnumValue generates a mock enum value (first valid value or "UNKNOWN").
func generateEnumValue(fieldDef *schema.FieldDef) string {
	if len(fieldDef.Values) > 0 {
		return fieldDef.Values[0]
	}
	return "UNKNOWN"
}

// RegisterMockHandlers registers mock handlers for all schemas in the registry.
// This populates the HandlerRegistry with mocks that can be used for dry-run validation.
//
// Each handler from the schema is converted to a mock and registered using
// the handler's full name (e.g., "position_keeping.initiate_log").
//
// Returns an error if registration fails for any handler.
func RegisterMockHandlers(handlerRegistry *saga.HandlerRegistry, schemaRegistry *schema.Registry) error {
	// Get all registered handler names from schema registry
	handlerNames := schemaRegistry.ListHandlers()

	for _, handlerName := range handlerNames {
		// Get handler definition from schema
		handlerDef, err := schemaRegistry.GetHandler(handlerName)
		if err != nil {
			return fmt.Errorf("failed to get handler %s: %w", handlerName, err)
		}

		// Generate mock handler
		mockHandler := GenerateMockHandler(handlerDef)

		// Register mock in handler registry
		if err := handlerRegistry.Register(handlerName, mockHandler); err != nil {
			return fmt.Errorf("failed to register mock handler %s: %w", handlerName, err)
		}
	}

	return nil
}

// extractStatusFromDescription attempts to extract a status value from field description.
// Handlers.yaml descriptions often contain the status value in parentheses, e.g.:
// "Status of the log entry (INITIATED)"
// "Status of the lien (ACTIVE)"
func extractStatusFromDescription(description string) string {
	// Look for pattern: (<STATUS_VALUE>)
	start := -1
	end := -1

	for i, ch := range description {
		if ch == '(' {
			start = i + 1
		} else if ch == ')' && start != -1 {
			end = i
			break
		}
	}

	if start != -1 && end != -1 && end > start {
		statusValue := description[start:end]
		// Verify it looks like a status (uppercase, underscores allowed)
		if isUppercaseWithUnderscores(statusValue) {
			return statusValue
		}
	}

	return ""
}

// isUppercaseWithUnderscores checks if a string contains only uppercase letters and underscores.
func isUppercaseWithUnderscores(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < 'A' || ch > 'Z' {
			if ch != '_' {
				return false
			}
		}
	}
	return true
}

// NewMockHandlerRegistry creates a new isolated HandlerRegistry populated with mocks.
// This is a convenience function for creating a registry dedicated to dry-run validation.
//
// The returned registry is independent from production handlers and safe to use
// for script validation without affecting real services.
//
// Example usage:
//
//	schemaRegistry := schema.NewRegistry()
//	schemaRegistry.LoadFromYAML(yamlBytes)
//	mockRegistry, err := NewMockHandlerRegistry(schemaRegistry)
//	// Use mockRegistry for dry-run saga validation
func NewMockHandlerRegistry(schemaRegistry *schema.Registry) (*saga.HandlerRegistry, error) {
	registry := saga.NewHandlerRegistry()

	if err := RegisterMockHandlers(registry, schemaRegistry); err != nil {
		return nil, fmt.Errorf("failed to populate mock handler registry: %w", err)
	}

	return registry, nil
}
