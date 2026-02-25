// Package server implements the MCP protocol handler, dispatching JSON-RPC 2.0
// method calls to the appropriate handlers.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/meridianhub/meridian/services/mcp-server/internal/prompts"
	"github.com/meridianhub/meridian/services/mcp-server/internal/resources"
	"github.com/meridianhub/meridian/services/mcp-server/internal/transport"
)

// ProtocolVersion is the MCP protocol version this server implements.
const ProtocolVersion = "2024-11-05"

// Info contains metadata about the MCP server.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities describes the server's MCP capabilities.
type Capabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

// ToolsCapability describes the server's tool-related capabilities.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability describes the server's resource-related capabilities.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability describes the server's prompt-related capabilities.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// InitializeResult is the response to the initialize method.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    Capabilities `json:"capabilities"`
	Info            Info         `json:"serverInfo"`
}

// Tool describes an MCP tool available for invocation.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolsListResult is the response to the tools/list method.
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// ToolCallParams are the parameters for the tools/call method.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolCallResult is the response to the tools/call method.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a content block in a tool result.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ToolHandler is a function that handles a tool invocation.
type ToolHandler func(ctx context.Context, arguments json.RawMessage) (*ToolCallResult, error)

// ResourcesListResult is the response to the resources/list method.
type ResourcesListResult struct {
	Resources []resources.Resource `json:"resources"`
}

// ResourceReadParams are the parameters for the resources/read method.
type ResourceReadParams struct {
	URI string `json:"uri"`
}

// ResourceReadResult is the response to the resources/read method.
type ResourceReadResult struct {
	Contents []resources.ResourceContent `json:"contents"`
}

// PromptsListResult is the response to the prompts/list method.
type PromptsListResult struct {
	Prompts []prompts.Prompt `json:"prompts"`
}

// PromptGetParams are the parameters for the prompts/get method.
type PromptGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// PromptGetResult is the response to the prompts/get method.
type PromptGetResult struct {
	Description string            `json:"description,omitempty"`
	Messages    []prompts.Message `json:"messages"`
}

// Config holds configuration for the MCP server.
type Config struct {
	ServerName    string
	ServerVersion string
}

// MCPServer implements the MCP protocol over a given transport.
type MCPServer struct {
	transport        transport.Transport
	config           Config
	logger           *slog.Logger
	tools            map[string]Tool
	handlers         map[string]ToolHandler
	resourceProvider *resources.Provider
	promptRegistry   *prompts.Registry
}

// New creates a new MCPServer.
func New(t transport.Transport, cfg Config, logger *slog.Logger) *MCPServer {
	return &MCPServer{
		transport: t,
		config:    cfg,
		logger:    logger,
		tools:     make(map[string]Tool),
		handlers:  make(map[string]ToolHandler),
	}
}

// SetResourceProvider attaches a resource provider to the server. It must be
// called before Run; calling it concurrently with Run is not safe.
func (s *MCPServer) SetResourceProvider(p *resources.Provider) {
	s.resourceProvider = p
}

// SetPromptRegistry attaches a prompt registry to the server. It must be
// called before Run; calling it concurrently with Run is not safe.
func (s *MCPServer) SetPromptRegistry(r *prompts.Registry) {
	s.promptRegistry = r
}

// RegisterTool registers a tool with the server. It must be called before Run;
// calling it concurrently with Run is not safe.
func (s *MCPServer) RegisterTool(tool Tool, handler ToolHandler) {
	s.tools[tool.Name] = tool
	s.handlers[tool.Name] = handler
}

// Run starts the server's message processing loop. It blocks until the context
// is cancelled or an unrecoverable error occurs.
func (s *MCPServer) Run(ctx context.Context) error {
	s.logger.Info("MCP server started", "name", s.config.ServerName, "version", s.config.ServerVersion)

	for {
		msg, err := s.transport.ReadMessage(ctx)
		if err != nil {
			// Context cancellation during read is a graceful shutdown.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				s.logger.Info("MCP server shutting down")
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}

		if msg.IsNotification() {
			s.handleNotification(msg)
			continue
		}

		if !msg.IsRequest() {
			s.logger.Warn("ignoring non-request message")
			continue
		}

		response := s.handleRequest(ctx, msg)
		if err := s.transport.WriteMessage(ctx, response); err != nil {
			s.logger.Error("failed to write response", "error", err)
		}
	}
}

func (s *MCPServer) handleNotification(msg *transport.JSONRPCMessage) {
	s.logger.Debug("received notification", "method", msg.Method)
}

func (s *MCPServer) handleRequest(ctx context.Context, msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	s.logger.Debug("handling request", "method", msg.Method, "id", string(msg.ID))

	switch msg.Method {
	case "initialize":
		return s.handleInitialize(msg)
	case "tools/list":
		return s.handleToolsList(msg)
	case "tools/call":
		return s.handleToolsCall(ctx, msg)
	case "resources/list":
		return s.handleResourcesList(msg)
	case "resources/read":
		return s.handleResourcesRead(ctx, msg)
	case "prompts/list":
		return s.handlePromptsList(msg)
	case "prompts/get":
		return s.handlePromptsGet(msg)
	default:
		return transport.NewErrorResponse(msg.ID, transport.CodeMethodNotFound,
			fmt.Sprintf("method not found: %s", msg.Method))
	}
}

func (s *MCPServer) handleInitialize(msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	caps := Capabilities{
		Tools: &ToolsCapability{},
	}
	if s.resourceProvider != nil {
		caps.Resources = &ResourcesCapability{}
	}
	if s.promptRegistry != nil {
		caps.Prompts = &PromptsCapability{}
	}

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    caps,
		Info: Info{
			Name:    s.config.ServerName,
			Version: s.config.ServerVersion,
		},
	}

	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handleToolsList(msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	tools := make([]Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	result := ToolsListResult{Tools: tools}
	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handleToolsCall(ctx context.Context, msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	var params ToolCallParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, "invalid tool call params")
	}

	handler, exists := s.handlers[params.Name]
	if !exists {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams,
			fmt.Sprintf("unknown tool: %s", params.Name))
	}

	result, err := handler(ctx, params.Arguments)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError,
			fmt.Sprintf("tool execution failed: %v", err))
	}

	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handleResourcesList(msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	if s.resourceProvider == nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeMethodNotFound, "resources not supported")
	}

	result := ResourcesListResult{Resources: s.resourceProvider.List()}
	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handleResourcesRead(ctx context.Context, msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	if s.resourceProvider == nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeMethodNotFound, "resources not supported")
	}

	var params ResourceReadParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, "invalid resource read params")
	}
	if params.URI == "" {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, "uri is required")
	}

	readResult, err := s.resourceProvider.Read(ctx, params.URI)
	if err != nil {
		if errors.Is(err, resources.ErrResourceNotFound) {
			return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams,
				fmt.Sprintf("resource not found: %q", params.URI))
		}
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError,
			fmt.Sprintf("resource read failed: %v", err))
	}

	result := ResourceReadResult{Contents: readResult.Contents}
	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handlePromptsList(msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	if s.promptRegistry == nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeMethodNotFound, "prompts not supported")
	}

	result := PromptsListResult{Prompts: s.promptRegistry.List()}
	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}

func (s *MCPServer) handlePromptsGet(msg *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	if s.promptRegistry == nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeMethodNotFound, "prompts not supported")
	}

	var params PromptGetParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, "invalid prompt get params")
	}
	if params.Name == "" {
		return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, "name is required")
	}

	getResult, err := s.promptRegistry.Get(params.Name, params.Arguments)
	if err != nil {
		if errors.Is(err, prompts.ErrPromptNotFound) {
			return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams,
				fmt.Sprintf("prompt not found: %q", params.Name))
		}
		if errors.Is(err, prompts.ErrMissingRequiredArgument) {
			return transport.NewErrorResponse(msg.ID, transport.CodeInvalidParams, err.Error())
		}
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError,
			fmt.Sprintf("prompt get failed: %v", err))
	}

	result := PromptGetResult{
		Description: getResult.Description,
		Messages:    getResult.Messages,
	}
	resp, err := transport.NewResponse(msg.ID, result)
	if err != nil {
		return transport.NewErrorResponse(msg.ID, transport.CodeInternalError, "failed to marshal response")
	}
	return resp
}
