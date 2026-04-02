// Package schema provides YAML-based handler schema definitions for saga orchestration.
// It enables compile-time validation and IDE support for handler references in Starlark scripts.
package schema

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FieldType represents the type of a handler parameter or return value.
type FieldType string

// Supported field types aligned with protobuf types where applicable.
const (
	TypeString  FieldType = "string"
	TypeInt32   FieldType = "int32"
	TypeInt64   FieldType = "int64"
	TypeUint32  FieldType = "uint32"
	TypeBool    FieldType = "bool"
	TypeDecimal FieldType = "Decimal" // maps to shopspring/decimal
	TypeEnum    FieldType = "enum"
	TypeArray   FieldType = "array"
	TypeMap     FieldType = "map"
	TypeUUID    FieldType = "uuid"
)

// validFieldTypes is the set of allowed field types.
var validFieldTypes = map[FieldType]bool{
	TypeString:  true,
	TypeInt32:   true,
	TypeInt64:   true,
	TypeUint32:  true,
	TypeBool:    true,
	TypeDecimal: true,
	TypeEnum:    true,
	TypeArray:   true,
	TypeMap:     true,
	TypeUUID:    true,
}

// ParseFieldType converts a string to a FieldType, validating it exists.
func ParseFieldType(s string) (FieldType, error) {
	ft := FieldType(s)
	if !validFieldTypes[ft] {
		return "", fmt.Errorf("%w: %q", ErrUnknownType, s)
	}
	return ft, nil
}

// CompensationStrategy declares how a handler handles compensation.
type CompensationStrategy string

// Valid compensation strategies for handler definitions.
const (
	CompensationStrategyAuto        CompensationStrategy = "auto"
	CompensationStrategySagaManaged CompensationStrategy = "saga_managed"
	CompensationStrategyNone        CompensationStrategy = "none"
)

var validCompensationStrategies = map[CompensationStrategy]bool{
	CompensationStrategyAuto:        true,
	CompensationStrategySagaManaged: true,
	CompensationStrategyNone:        true,
}

// Schema errors.
var (
	ErrServiceRequired              = errors.New("service is required")
	ErrUnknownType                  = errors.New("unknown type")
	ErrEnumRequiresValues           = errors.New("enum type requires values")
	ErrHandlerNotFound              = errors.New("handler not found")
	ErrMissingRequiredParam         = errors.New("missing required parameter")
	ErrInvalidEnumValue             = errors.New("invalid enum value")
	ErrMissingCompensationStrategy  = errors.New("handler must declare either 'compensate' or 'compensation_strategy'")
	ErrInvalidCompensationStrategy  = errors.New("invalid compensation_strategy value")
	ErrConflictCompensationStrategy = errors.New("handler with 'compensate' should not set 'compensation_strategy' to non-auto value")
	ErrInvalidConversionRule        = errors.New("invalid conversion rule")
	ErrDeprecatedHandler            = errors.New("deprecated handler")
	ErrOverrideMissingType          = errors.New("ParamOverride for non-proto field requires explicit Type")
	ErrOverrideAliasCollision       = errors.New("ParamOverride alias would overwrite existing field")
	ErrProtoServiceNotFound         = errors.New("proto service not found in registry")
	ErrProtoMethodNotFound          = errors.New("proto method not found in service")
	ErrProtoFieldPathNotFound       = errors.New("proto field path not found in message")
	ErrInvalidProtoRPC              = errors.New("proto_rpc must be in format 'package.Service/Method'")
	ErrUnknownAliasSource           = errors.New("param_alias references unknown field")
	ErrAliasCollision               = errors.New("param_alias target already exists as a field")
	ErrDuplicateLeafName            = errors.New("duplicate leaf field name from exposed paths")
)

// Schema represents a collection of handler definitions for a service.
type Schema struct {
	// Service is the service name (e.g., "current_account").
	Service string `yaml:"service"`

	// Version is the schema version (e.g., "1.0").
	Version string `yaml:"version"`

	// Handlers maps handler names to their definitions.
	Handlers map[string]*HandlerDef `yaml:"handlers"`
}

// ProtoReference maps a handler to its proto RPC definition for slim schema format.
// When present, Params and Returns are resolved from proto reflection at load time
// rather than being defined inline in YAML.
type ProtoReference struct {
	// FullMethod is the proto RPC path, e.g.,
	// "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog".
	FullMethod string `yaml:"proto_rpc"`

	// ExposedParams lists proto request field paths exposed to Starlark scripts.
	// Simple fields: ["account_id", "amount"]. Nested: ["log.status_tracking.current_status"].
	ExposedParams []string `yaml:"exposed_params,omitempty"`

	// ExposedReturns lists proto response field paths exposed to Starlark scripts.
	ExposedReturns []string `yaml:"exposed_returns,omitempty"`

	// ParamAliases maps proto field names to Starlark-visible aliases.
	// Example: {"account_id": "position_id"} renames account_id to position_id in Starlark.
	ParamAliases map[string]string `yaml:"param_aliases,omitempty"`
}

// HandlerDef defines the schema for a single handler.
type HandlerDef struct {
	// Description provides human-readable documentation.
	Description string `yaml:"description"`

	// ProtoRef holds the proto RPC reference for slim schema format.
	// When set, Params and Returns are resolved from proto reflection at load time.
	// Mutually exclusive with inline Params/Returns definitions in YAML.
	ProtoRef *ProtoReference `yaml:"proto_ref,omitempty"`

	// Params defines the input parameters.
	// For proto-referenced handlers, populated by ResolveProtoTypes.
	Params map[string]*FieldDef `yaml:"params"`

	// Returns defines the return value fields.
	// For proto-referenced handlers, populated by ResolveProtoTypes.
	Returns map[string]*FieldDef `yaml:"returns"`

	// Compensate is the handler name used for compensation/rollback.
	Compensate string `yaml:"compensate,omitempty"`

	// External indicates this handler calls external systems (non-idempotent).
	// External handlers must have verify_external_state() called before invocation.
	External bool `yaml:"external,omitempty"`

	// CompensationStrategy declares how compensation is handled when no compensate handler is set.
	// Valid values: "auto" (implicit when compensate is set), "saga_managed", "none".
	CompensationStrategy CompensationStrategy `yaml:"compensation_strategy,omitempty"`

	// ResourceType identifies the RBAC resource type for authorization checks.
	// When set, the saga runtime checks Claims before allowing invocation.
	ResourceType string `yaml:"resource_type,omitempty"`

	// RequiredPermission is the RBAC permission required to invoke this handler.
	// Only checked when ResourceType is non-empty.
	RequiredPermission string `yaml:"required_permission,omitempty"`

	// Version is the handler version number. Defaults to 1 if unset.
	Version int `yaml:"version,omitempty"`

	// Composite marks this handler as a composite handler - one that orchestrates
	// multiple underlying operations and has no single proto request/response shape.
	// Composite handlers use params: {} intentionally and skip proto_ref validation.
	Composite bool `yaml:"composite,omitempty"`

	// IsDeprecated marks this handler as deprecated. Scripts calling it will receive a warning.
	Deprecated bool `yaml:"deprecated,omitempty"`

	// Conversions defines rules for converting calls from previous handler versions.
	Conversions []ConversionRule `yaml:"conversions,omitempty"`
}

// ConversionRule defines how to convert a call from a deprecated handler to the current version.
type ConversionRule struct {
	// FromVersion is the version being converted from.
	FromVersion int `yaml:"from_version"`

	// FromName is the old handler name (if renamed).
	FromName string `yaml:"from_name,omitempty"`

	// ParamMapping maps old parameter names to new parameter names.
	// Key: new param name, Value: old param name.
	ParamMapping map[string]string `yaml:"param_mapping,omitempty"`

	// Defaults provides default values for new parameters not present in old versions.
	// Key: param name, Value: default value expression.
	Defaults map[string]string `yaml:"defaults,omitempty"`

	// Sunset is the version at which the old handler will be removed entirely.
	Sunset string `yaml:"sunset,omitempty"`
}

// FieldDef defines a single field (parameter or return value).
type FieldDef struct {
	// Type is the field type.
	Type FieldType `yaml:"type"`

	// Required indicates if the field must be provided.
	Required bool `yaml:"required"`

	// Description provides human-readable documentation.
	Description string `yaml:"description,omitempty"`

	// Values lists allowed values for enum types.
	Values []string `yaml:"values,omitempty"`

	// ItemType specifies the element type for array types.
	ItemType FieldType `yaml:"item_type,omitempty"`

	// KeyType specifies the key type for map types.
	KeyType FieldType `yaml:"key_type,omitempty"`

	// ValueType specifies the value type for map types.
	ValueType FieldType `yaml:"value_type,omitempty"`
}

// Parse parses YAML bytes into a Schema, validating the structure.
func Parse(data []byte) (*Schema, error) {
	var schema Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := schema.Validate(); err != nil {
		return nil, err
	}

	return &schema, nil
}

// Validate checks that the schema is well-formed.
func (s *Schema) Validate() error {
	if s.Service == "" {
		return ErrServiceRequired
	}

	for handlerName, handler := range s.Handlers {
		if err := handler.Validate(handlerName); err != nil {
			return err
		}
	}

	return nil
}

// Validate checks that the handler definition is well-formed.
func (h *HandlerDef) Validate(handlerName string) error {
	// Normalize version: unset (0) defaults to 1
	if h.Version == 0 {
		h.Version = 1
	}

	// Composite handlers must not declare proto_ref.
	if h.Composite && h.ProtoRef != nil {
		return fmt.Errorf("handler %s: composite handler must not set proto_ref", handlerName)
	}

	// Proto-referenced handlers: validate the reference format.
	// Params/Returns are resolved later via ResolveProtoTypes, so skip field validation.
	if h.ProtoRef != nil {
		if err := h.ProtoRef.validate(handlerName); err != nil {
			return err
		}
	}

	// Only validate inline params/returns for non-proto-referenced handlers
	if h.ProtoRef == nil {
		for fieldName, field := range h.Params {
			if err := field.Validate(fmt.Sprintf("%s.params.%s", handlerName, fieldName)); err != nil {
				return err
			}
		}

		for fieldName, field := range h.Returns {
			if err := field.Validate(fmt.Sprintf("%s.returns.%s", handlerName, fieldName)); err != nil {
				return err
			}
		}
	}

	// Validate RBAC metadata: require both or neither
	if (h.ResourceType == "") != (h.RequiredPermission == "") {
		return fmt.Errorf("handler %s: %w", handlerName, ErrPartialRBACMetadata)
	}

	if err := h.validateCompensation(handlerName); err != nil {
		return err
	}

	for i := range h.Conversions {
		if err := h.validateConversionRule(handlerName, i); err != nil {
			return err
		}
	}

	return nil
}

// validate checks that a ProtoReference is well-formed.
func (pr *ProtoReference) validate(handlerName string) error {
	if pr.FullMethod == "" {
		return fmt.Errorf("%w: handler %s has empty proto_rpc", ErrInvalidProtoRPC, handlerName)
	}
	// Must contain exactly one '/' separating service from method
	parts := strings.SplitN(pr.FullMethod, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("%w: handler %s has %q", ErrInvalidProtoRPC, handlerName, pr.FullMethod)
	}
	return nil
}

// validateCompensation checks compensation coverage: every handler must declare
// either compensate or compensation_strategy, and the values must be consistent.
func (h *HandlerDef) validateCompensation(handlerName string) error {
	if h.Compensate == "" && h.CompensationStrategy == "" {
		return fmt.Errorf("%w: %s", ErrMissingCompensationStrategy, handlerName)
	}
	if h.CompensationStrategy != "" && !validCompensationStrategies[h.CompensationStrategy] {
		return fmt.Errorf("%w: %s has %q", ErrInvalidCompensationStrategy, handlerName, h.CompensationStrategy)
	}
	if h.Compensate == "" && h.CompensationStrategy == CompensationStrategyAuto {
		return fmt.Errorf("%w: %s has %q without compensate", ErrInvalidCompensationStrategy, handlerName, h.CompensationStrategy)
	}
	if h.Compensate != "" && h.CompensationStrategy != "" && h.CompensationStrategy != CompensationStrategyAuto {
		return fmt.Errorf("%w: %s", ErrConflictCompensationStrategy, handlerName)
	}
	return nil
}

// validateConversionRule checks a single conversion rule is well-formed.
func (h *HandlerDef) validateConversionRule(handlerName string, index int) error {
	conv := h.Conversions[index]

	if conv.FromVersion <= 0 {
		return fmt.Errorf("%w: %s conversions[%d] from_version must be positive", ErrInvalidConversionRule, handlerName, index)
	}
	if h.Version > 0 && conv.FromVersion >= h.Version {
		return fmt.Errorf("%w: %s conversions[%d] from_version (%d) must be less than current version (%d)",
			ErrInvalidConversionRule, handlerName, index, conv.FromVersion, h.Version)
	}
	for newParam := range conv.ParamMapping {
		if _, exists := h.Params[newParam]; !exists {
			return fmt.Errorf("%w: %s conversions[%d] param_mapping references unknown parameter %q",
				ErrInvalidConversionRule, handlerName, index, newParam)
		}
	}
	for defaultParam := range conv.Defaults {
		if _, exists := h.Params[defaultParam]; !exists {
			return fmt.Errorf("%w: %s conversions[%d] defaults references unknown parameter %q",
				ErrInvalidConversionRule, handlerName, index, defaultParam)
		}
	}
	return nil
}

// Validate checks that the field definition is well-formed.
func (f *FieldDef) Validate(context string) error {
	if !validFieldTypes[f.Type] {
		return fmt.Errorf("%w: %s has type %q", ErrUnknownType, context, f.Type)
	}

	if f.Type == TypeEnum && len(f.Values) == 0 {
		return fmt.Errorf("%w: %s", ErrEnumRequiresValues, context)
	}

	return nil
}

// ValidateParams validates that the provided params match the handler schema.
func (h *HandlerDef) ValidateParams(params map[string]any) error {
	if err := h.validateRequiredParams(params); err != nil {
		return err
	}
	return h.validateEnumParams(params)
}

// validateRequiredParams checks all required fields are present.
func (h *HandlerDef) validateRequiredParams(params map[string]any) error {
	for name, field := range h.Params {
		if field.Required {
			if _, ok := params[name]; !ok {
				return fmt.Errorf("%w: %s", ErrMissingRequiredParam, name)
			}
		}
	}
	return nil
}

// validateEnumParams validates enum field values against allowed values.
func (h *HandlerDef) validateEnumParams(params map[string]any) error {
	for name, field := range h.Params {
		if err := field.validateEnumValue(name, params); err != nil {
			return err
		}
	}
	return nil
}

// validateEnumValue validates a single enum field if applicable.
func (f *FieldDef) validateEnumValue(name string, params map[string]any) error {
	if f.Type != TypeEnum || len(f.Values) == 0 {
		return nil
	}

	val, ok := params[name]
	if !ok {
		return nil
	}

	strVal, ok := val.(string)
	if !ok {
		return fmt.Errorf("%w: %s must be string", ErrInvalidEnumValue, name)
	}

	for _, allowed := range f.Values {
		if strVal == allowed {
			return nil
		}
	}
	return fmt.Errorf("%w: %s got %q, allowed: %v", ErrInvalidEnumValue, name, strVal, f.Values)
}
