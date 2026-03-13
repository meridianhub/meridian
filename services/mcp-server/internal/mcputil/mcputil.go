// Package mcputil provides helper functions for building MCP SDK responses.
package mcputil

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc/status"
)

// TextResult returns a CallToolResult with a single text content block.
func TextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// JSONResult marshals v to JSON and returns it as a text content block.
func JSONResult(v interface{}) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result: %w", err)
	}
	return TextResult(string(data)), nil
}

// ErrorResult returns a CallToolResult marked as an error with a text message.
func ErrorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}
}

// FormatError builds an ErrorResult with a formatted message.
func FormatError(format string, args ...interface{}) *mcp.CallToolResult {
	return ErrorResult(fmt.Sprintf(format, args...))
}

// SanitizeError extracts a user-safe message from the error.
// gRPC status errors are reduced to their message; other errors pass through
// as-is since they originate from local validation (CEL, Starlark, etc.).
func SanitizeError(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Message()
	}
	return err.Error()
}
