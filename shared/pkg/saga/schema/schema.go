// Package schema provides YAML-based handler schema definitions for saga orchestration.
// It enables compile-time validation and IDE support for handler references in Starlark scripts.
package schema

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
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
		return fmt.Errorf("handler %s: resource_type and required_permission must both be set or both be empty", handlerName)
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
				if existing, exists := r.deprecatedNames[conv.FromName]; exists {
					return fmt.Errorf(
						"%w: duplicate deprecated alias %q maps to both %s and %s",
						ErrInvalidConversionRule, conv.FromName, existing.CurrentName, name,
					)
				}
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

// ToSchema returns a Schema snapshot from the registry's current state.
// This enables callers that have a YAML-based Registry to use BuildServiceModulesFromSchema.
func (r *Registry) ToSchema() *Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handlers := make(map[string]*HandlerDef, len(r.handlers))
	for name, def := range r.handlers {
		handlers[name] = def
	}
	return &Schema{Handlers: handlers}
}

// NewRegistryFromSchema creates a Registry pre-populated with handler definitions from a Schema.
// This is the inverse of ToSchema() and enables creating a Registry from proto-derived schemas.
// It also rebuilds the deprecatedNames index from conversion rules that specify FromName.
func NewRegistryFromSchema(s *Schema) *Registry {
	r := NewRegistry()
	if s != nil {
		for name, def := range s.Handlers {
			r.handlers[name] = def
			// Rebuild deprecated name mappings from conversion rules
			for i := range def.Conversions {
				conv := &def.Conversions[i]
				if conv.FromName != "" {
					r.deprecatedNames[conv.FromName] = &DeprecatedMapping{
						CurrentName:    name,
						ConversionRule: conv,
					}
				}
			}
		}
	}
	return r
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

// ResolveProtoTypes resolves proto-referenced handlers in the schema by looking up
// proto service/method descriptors and populating Params/Returns from proto reflection.
// Handlers without ProtoRef are left unchanged (legacy inline format).
// Uses the global proto registry by default; pass a custom resolver for testing.
func (s *Schema) ResolveProtoTypes(files *protoregistry.Files) error {
	if files == nil {
		files = protoregistry.GlobalFiles
	}
	for handlerName, handler := range s.Handlers {
		if handler.ProtoRef == nil {
			continue
		}
		if err := resolveHandlerProto(handlerName, handler, files); err != nil {
			return fmt.Errorf("handler %s: %w", handlerName, err)
		}
	}
	return nil
}

// resolveHandlerProto resolves a single handler's proto reference into Params/Returns.
func resolveHandlerProto(_ string, handler *HandlerDef, files *protoregistry.Files) error {
	ref := handler.ProtoRef

	// Parse "package.Service/Method" into service and method names
	parts := strings.SplitN(ref.FullMethod, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: %s", ErrInvalidProtoRPC, ref.FullMethod)
	}
	serviceFQN := protoreflect.FullName(parts[0])
	methodName := protoreflect.Name(parts[1])

	// Look up the service descriptor
	serviceDesc, err := findServiceDescriptor(files, serviceFQN)
	if err != nil {
		return err
	}

	// Look up the method
	methodDesc := serviceDesc.Methods().ByName(methodName)
	if methodDesc == nil {
		return fmt.Errorf("%w: %s in service %s", ErrProtoMethodNotFound, methodName, serviceFQN)
	}

	// Resolve params from request message
	reqMsg := methodDesc.Input()
	params, err := resolveExposedFields(reqMsg, ref.ExposedParams, ref.ParamAliases)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	handler.Params = params

	// Resolve returns from response message
	respMsg := methodDesc.Output()
	returns, err := resolveExposedFields(respMsg, ref.ExposedReturns, nil)
	if err != nil {
		return fmt.Errorf("returns: %w", err)
	}
	handler.Returns = returns

	return nil
}

// findServiceDescriptor searches the proto registry for a service by fully-qualified name.
func findServiceDescriptor(files *protoregistry.Files, fqn protoreflect.FullName) (protoreflect.ServiceDescriptor, error) {
	desc, err := files.FindDescriptorByName(fqn)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%w)", ErrProtoServiceNotFound, fqn, err)
	}
	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a service descriptor", ErrProtoServiceNotFound, fqn)
	}
	return sd, nil
}

// resolveExposedFields builds a FieldDef map from a proto message descriptor,
// filtered to only the exposed field paths. If exposed is nil/empty, all top-level
// fields are included. Aliases are applied to param fields.
// Returns ErrProtoFieldPathNotFound if any exposed path cannot be resolved.
func resolveExposedFields(md protoreflect.MessageDescriptor, exposed []string, aliases map[string]string) (map[string]*FieldDef, error) {
	fields := make(map[string]*FieldDef)

	if len(exposed) == 0 {
		// Include all top-level fields
		fds := md.Fields()
		for i := 0; i < fds.Len(); i++ {
			fd := fds.Get(i)
			fieldName := string(fd.Name())
			fields[fieldName] = deriveFieldDef(fd)
		}
	} else {
		// Only include exposed field paths
		for _, path := range exposed {
			fd := resolveFieldPath(md, path)
			if fd == nil {
				return nil, fmt.Errorf("%w: %q in message %s", ErrProtoFieldPathNotFound, path, md.FullName())
			}
			// Use the leaf field name as the key
			leafName := leafFieldName(path)
			if _, dup := fields[leafName]; dup {
				return nil, fmt.Errorf("%w: %q from path %q in message %s", ErrDuplicateLeafName, leafName, path, md.FullName())
			}
			fields[leafName] = deriveFieldDef(fd)
		}
	}

	// Apply aliases with validation
	for original, alias := range aliases {
		def, ok := fields[original]
		if !ok {
			return nil, fmt.Errorf("%w: %q (alias target: %q)", ErrUnknownAliasSource, original, alias)
		}
		if _, collision := fields[alias]; collision && alias != original {
			return nil, fmt.Errorf("%w: %q (from alias of %q)", ErrAliasCollision, alias, original)
		}
		delete(fields, original)
		fields[alias] = def
	}

	return fields, nil
}

// resolveFieldPath resolves a dot-separated field path (e.g., "log.status_tracking.current_status")
// through nested proto message descriptors. Returns the leaf field descriptor, or nil if not found.
func resolveFieldPath(md protoreflect.MessageDescriptor, path string) protoreflect.FieldDescriptor {
	parts := strings.Split(path, ".")
	current := md

	for i, part := range parts {
		fd := current.Fields().ByName(protoreflect.Name(part))
		if fd == nil {
			return nil
		}
		// If this is the last part, return the field descriptor
		if i == len(parts)-1 {
			return fd
		}
		// Otherwise, navigate into the nested message
		if fd.Kind() != protoreflect.MessageKind {
			return nil // Can't traverse into non-message field
		}
		current = fd.Message()
	}
	return nil
}

// leafFieldName returns the last segment of a dot-separated path.
func leafFieldName(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// HasProtoRef returns true if the handler uses proto-referenced format.
func (h *HandlerDef) HasProtoRef() bool {
	return h.ProtoRef != nil
}
