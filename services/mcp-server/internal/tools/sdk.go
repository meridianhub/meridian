// Package tools provides the tool registry for the MCP server.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/meridianhub/meridian/services/mcp-server/internal/mcputil"
)

// ToolHandler is a function invoked to execute a tool call.
// params contains the validated JSON arguments.
type ToolHandler func(ctx context.Context, params json.RawMessage) (interface{}, error)

// ToolCategory classifies the operational intent of a tool.
type ToolCategory int

const (
	// CategoryRead tools query state without side effects.
	CategoryRead ToolCategory = iota
	// CategorySimulate tools compute or preview without persisting changes.
	CategorySimulate
	// CategoryWrite tools mutate state in the system.
	CategoryWrite
)

// Tool describes an MCP tool with its metadata, schema, and handler.
// This is an internal data transfer type used by build functions; tools are
// registered onto the SDK server via addTool.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
	Category    ToolCategory
	Handler     ToolHandler
}

// compileSchema compiles a map-based JSON Schema into a validator.
func compileSchema(schema map[string]interface{}) *jsonschema.Schema {
	if schema == nil {
		return nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(data)); err != nil {
		return nil
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		return nil
	}
	return compiled
}

// addTool registers a Tool on the SDK server, wrapping its handler so that
// the return value is JSON-serialized into an MCP CallToolResult.
// Input is validated against the tool's JSON Schema before the handler is called.
func addTool(srv *mcp.Server, t Tool) {
	handler := t.Handler                      // capture for closure
	validator := compileSchema(t.InputSchema) // compile schema once
	srv.AddTool(&mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.InputSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Validate input against JSON Schema.
		args := req.Params.Arguments
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		if validator != nil {
			var v interface{}
			if err := json.Unmarshal(args, &v); err != nil {
				return mcputil.ErrorResult(fmt.Sprintf("invalid JSON arguments: %v", err)), nil
			}
			if err := validator.Validate(v); err != nil {
				return mcputil.ErrorResult(fmt.Sprintf("validation error: %v", err)), nil
			}
		}

		result, err := handler(ctx, args)
		if err != nil {
			return mcputil.ErrorResult(mcputil.SanitizeError(err)), nil
		}
		return mcputil.JSONResult(result)
	})
}
