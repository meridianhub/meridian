package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

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
// If proto-referenced handlers are present, call ResolveProtoTypes on the
// returned schema after loading to populate Params/Returns from proto reflection.
func (r *Registry) LoadFromYAML(data []byte) error {
	schema, err := Parse(data)
	if err != nil {
		return err
	}

	// Proto resolution is deferred to callers that need it. Callers requiring
	// proto-backed param validation must call ResolveProtoTypes on the schema
	// after loading. Resolving eagerly here fails when proto descriptors aren't
	// registered in the global registry (e.g. tools/saga-doc-gen which generates
	// documentation from YAML without importing proto packages).

	// Build temporary maps for the new schema's handlers and deprecated aliases.
	tempHandlers := make(map[string]*HandlerDef, len(schema.Handlers))
	tempDeprecated := make(map[string]*DeprecatedMapping)

	for name, handler := range schema.Handlers {
		tempHandlers[name] = handler
		for i := range handler.Conversions {
			conv := &handler.Conversions[i]
			if conv.FromName == "" {
				continue
			}
			// Check for duplicates within the new batch
			if existing, exists := tempDeprecated[conv.FromName]; exists {
				return fmt.Errorf(
					"%w: duplicate deprecated alias %q maps to both %s and %s",
					ErrInvalidConversionRule, conv.FromName, existing.CurrentName, name,
				)
			}
			tempDeprecated[conv.FromName] = &DeprecatedMapping{
				CurrentName:    name,
				ConversionRule: conv,
			}
		}
	}

	// Validate against existing registry state and merge atomically under the lock.
	r.mu.Lock()
	defer r.mu.Unlock()

	for oldName, mapping := range tempDeprecated {
		if existing, exists := r.deprecatedNames[oldName]; exists {
			return fmt.Errorf(
				"%w: duplicate deprecated alias %q maps to both %s and %s",
				ErrInvalidConversionRule, oldName, existing.CurrentName, mapping.CurrentName,
			)
		}
	}

	r.schemas = append(r.schemas, schema)
	for name, handler := range tempHandlers {
		r.handlers[name] = handler
	}
	for oldName, mapping := range tempDeprecated {
		r.deprecatedNames[oldName] = mapping
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
//
// The caller is responsible for ensuring proto-referenced handlers have already been resolved
// (via ResolveProtoTypes or DeriveSchemaFromProto) before passing the schema here. All current
// callers use derived schemas where proto types are already populated.
func NewRegistryFromSchema(s *Schema) *Registry {
	r := NewRegistry()
	if s != nil {
		r.schemas = append(r.schemas, s)
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
