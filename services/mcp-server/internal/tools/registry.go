// Package tools provides the tool registry for the MCP server.
// It manages tool registration, JSON schema validation, and dispatch to
// handler functions.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// ErrToolNotFound is returned when a tool name is not registered.
var ErrToolNotFound = errors.New("unknown tool")

// ToolCategory classifies the operational intent of a tool.
// Clients can use this to apply policies (e.g., require confirmation for writes).
type ToolCategory int

const (
	// CategoryRead tools query state without side effects.
	CategoryRead ToolCategory = iota
	// CategorySimulate tools compute or preview without persisting changes.
	CategorySimulate
	// CategoryWrite tools mutate state in the system.
	CategoryWrite
)

// ToolHandler is a function invoked to execute a tool call.
// params contains the validated JSON arguments.
type ToolHandler func(ctx context.Context, params json.RawMessage) (interface{}, error)

// Tool describes an MCP tool with its metadata, schema, and handler.
// Handler is excluded from JSON serialization; callers use List() to get
// metadata suitable for the tools/list response.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Category    ToolCategory           `json:"-"`
	Handler     ToolHandler            `json:"-"`
	validator   *jsonschema.Schema
}

// Registry is a thread-safe collection of MCP tools.
// JSON schemas are compiled once at registration time and reused per call.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]*Tool),
	}
}

// Register adds a tool to the registry, compiling its JSON schema.
// Returns an error if the schema is invalid or compilation fails.
// Registering a tool with a name that already exists overwrites the previous entry.
func (r *Registry) Register(tool Tool) error {
	compiled, err := compileSchema(tool.InputSchema)
	if err != nil {
		return fmt.Errorf("compile schema for tool %q: %w", tool.Name, err)
	}
	tool.validator = compiled

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = &tool
	return nil
}

// Call looks up the named tool, validates params against its JSON schema,
// and invokes the handler. Returns an error if the tool is not found,
// validation fails, or the handler returns an error.
func (r *Registry) Call(ctx context.Context, name string, params json.RawMessage) (interface{}, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrToolNotFound, name)
	}

	if err := validateParams(tool.validator, params); err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: %w", name, err)
	}

	return tool.Handler(ctx, params)
}

// List returns a snapshot of registered tool metadata (without handlers or
// compiled validators). The slice is sorted by tool name for stable output.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Category:    t.Category,
		})
	}

	// Stable sort by name.
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].Name < result[j-1].Name; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}

	return result
}

// compileSchema compiles a JSON Schema from a Go map.
// The schema is serialized to JSON and compiled via the jsonschema library.
func compileSchema(schema map[string]interface{}) (*jsonschema.Schema, error) {
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	const schemaURL = "schema.json"
	if err := compiler.AddResource(schemaURL, bytes.NewReader(schemaBytes)); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}

	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	return compiled, nil
}

// validateParams validates raw JSON params against the compiled schema.
func validateParams(validator *jsonschema.Schema, params json.RawMessage) error {
	// Treat nil/empty params as an empty object for schema validation.
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}

	var v interface{}
	if err := json.Unmarshal(params, &v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	return validator.Validate(v)
}
