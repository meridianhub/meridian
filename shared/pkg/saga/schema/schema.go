// Package schema provides YAML-based handler schema definitions for saga orchestration.
// It enables compile-time validation and IDE support for handler references in Starlark scripts.
package schema

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed handlers.yaml
var embeddedPlatformHandlers []byte

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

// HandlerDef defines the schema for a single handler.
type HandlerDef struct {
	// Description provides human-readable documentation.
	Description string `yaml:"description"`

	// Params defines the input parameters.
	Params map[string]*FieldDef `yaml:"params"`

	// Returns defines the return value fields.
	Returns map[string]*FieldDef `yaml:"returns"`

	// Compensate is the handler name used for compensation/rollback.
	Compensate string `yaml:"compensate,omitempty"`

	// External indicates this handler calls external systems (non-idempotent).
	// External handlers must have verify_external_state() called before invocation.
	External bool `yaml:"external,omitempty"`

	// CompensationStrategy declares how compensation is handled when no compensate handler is set.
	// Valid values: "auto" (implicit when compensate is set), "saga_managed", "none".
	CompensationStrategy CompensationStrategy `yaml:"compensation_strategy,omitempty"`

	// Version is the handler version number. Defaults to 1 if unset.
	Version int `yaml:"version,omitempty"`

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

// DeprecatedMapping records how a deprecated handler name maps to its current replacement.
type DeprecatedMapping struct {
	// CurrentName is the fully-qualified name of the current handler.
	CurrentName string

	// ConversionRule is the conversion rule that applies.
	ConversionRule *ConversionRule
}

// Registry manages multiple handler schemas.
type Registry struct {
	mu              sync.RWMutex
	schemas         []*Schema
	handlers        map[string]*HandlerDef
	deprecatedNames map[string]*DeprecatedMapping // old name -> current handler mapping
}

// NewRegistry creates a new empty schema registry.
func NewRegistry() *Registry {
	return &Registry{
		schemas:         make([]*Schema, 0),
		handlers:        make(map[string]*HandlerDef),
		deprecatedNames: make(map[string]*DeprecatedMapping),
	}
}

// LoadFromYAML parses and loads a schema from YAML bytes.
func (r *Registry) LoadFromYAML(data []byte) error {
	schema, err := Parse(data)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.schemas = append(r.schemas, schema)
	for name, handler := range schema.Handlers {
		r.handlers[name] = handler
		// Index deprecated name mappings from conversion rules
		for i := range handler.Conversions {
			conv := &handler.Conversions[i]
			if conv.FromName != "" {
				r.deprecatedNames[conv.FromName] = &DeprecatedMapping{
					CurrentName:    name,
					ConversionRule: conv,
				}
			}
		}
	}

	return nil
}

// GetHandler returns the handler definition for the given name.
func (r *Registry) GetHandler(name string) (*HandlerDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotFound, name)
	}
	return handler, nil
}

// ListHandlers returns a sorted list of all registered handler names.
func (r *Registry) ListHandlers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasHandler returns true if the handler is registered.
func (r *Registry) HasHandler(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.handlers[name]
	return ok
}

// LookupDeprecated checks if a handler name is deprecated and returns
// the mapping to the current handler, or nil if the name is not deprecated.
func (r *Registry) LookupDeprecated(name string) *DeprecatedMapping {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.deprecatedNames[name]
}

// IsDeprecated returns true if the handler exists but is marked as deprecated.
func (r *Registry) IsDeprecated(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, ok := r.handlers[name]
	if !ok {
		return false
	}
	return handler.Deprecated
}

// LoadFromFile loads a schema from a YAML file.
func (r *Registry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", path, err)
	}
	return r.LoadFromYAML(data)
}

// ListSchemas returns all loaded schemas in the registry.
func (r *Registry) ListSchemas() []*Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Schema, len(r.schemas))
	copy(result, r.schemas)
	return result
}

// DefaultRegistry creates a schema registry pre-loaded with the embedded platform handlers.yaml.
// This provides the standard handler schema for Starlark saga validation and tooling.
func DefaultRegistry() (*Registry, error) {
	reg := NewRegistry()
	if err := reg.LoadFromYAML(embeddedPlatformHandlers); err != nil {
		return nil, fmt.Errorf("failed to load platform handlers: %w", err)
	}
	return reg, nil
}

// LoadFromDirectory loads all YAML schema files from a directory.
// Files must have .yaml or .yml extension. Subdirectories are not traversed.
func (r *Registry) LoadFromDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read schema directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		if err := r.LoadFromFile(path); err != nil {
			return fmt.Errorf("failed to load schema %s: %w", path, err)
		}
	}

	return nil
}

// ValidateHandlerParams validates parameters for a named handler.
// Returns ErrHandlerNotFound if the handler schema is not registered.
func (r *Registry) ValidateHandlerParams(handlerName string, params map[string]any) error {
	handler, err := r.GetHandler(handlerName)
	if err != nil {
		return err
	}
	return handler.ValidateParams(params)
}

// LinterMetadata describes handler characteristics needed by the semantic linter.
type LinterMetadata struct {
	// IsExternal indicates the handler calls external systems (non-idempotent).
	IsExternal bool

	// RequiresPreCheck indicates verify_external_state must be called before this handler.
	RequiresPreCheck bool

	// CompensationStrategy indicates how compensation is handled ("auto", "saga_managed", "none", or "").
	CompensationStrategy string

	// HasAutoCompensation indicates the handler has a compensate: field.
	HasAutoCompensation bool

	// IsDeprecated indicates the handler is deprecated and should produce a warning.
	IsDeprecated bool

	// ReplacedBy is the current handler name if this handler has been superseded.
	ReplacedBy string
}

// BuildLinterMetadata extracts linter metadata from the schema registry.
// Returns a map of handler names to their metadata for linter validation.
// All handlers are included to support compensation coverage checks.
func (r *Registry) BuildLinterMetadata() map[string]LinterMetadata {
	metadata := make(map[string]LinterMetadata)

	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, handler := range r.handlers {
		meta := LinterMetadata{}
		if handler.External {
			meta.IsExternal = true
			meta.RequiresPreCheck = true
		}
		if handler.Compensate != "" {
			meta.HasAutoCompensation = true
			meta.CompensationStrategy = string(CompensationStrategyAuto)
		} else {
			meta.CompensationStrategy = string(handler.CompensationStrategy)
		}
		if handler.Deprecated {
			meta.IsDeprecated = true
		}
		metadata[name] = meta
	}

	// Also add entries for deprecated name aliases so they can be detected
	for oldName, mapping := range r.deprecatedNames {
		if _, exists := metadata[oldName]; !exists {
			// Get the current handler's metadata as a base
			if currentMeta, ok := metadata[mapping.CurrentName]; ok {
				deprecatedMeta := currentMeta
				deprecatedMeta.IsDeprecated = true
				deprecatedMeta.ReplacedBy = mapping.CurrentName
				metadata[oldName] = deprecatedMeta
			}
		}
	}

	return metadata
}
