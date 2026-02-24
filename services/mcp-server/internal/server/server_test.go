package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/transport"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// sendAndReceive sends a JSON-RPC request via stdio transport and reads the response.
func sendAndReceive(t *testing.T, s *MCPServer, request *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	t.Helper()

	// Encode request
	reqData, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reqData = append(reqData, '\n')

	reader := bytes.NewReader(reqData)
	writer := &bytes.Buffer{}
	tr := transport.NewStdioTransport(reader, writer)

	srv := New(tr, s.config, s.logger)
	// Copy registered tools
	for name, tool := range s.tools {
		srv.tools[name] = tool
		srv.handlers[name] = s.handlers[name]
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Run server - it will read one message, process it, write response, then fail on next read (EOF)
	_ = srv.Run(ctx)

	// Parse response
	output := writer.String()
	if output == "" {
		t.Fatal("no response written")
	}

	var resp transport.JSONRPCMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (output: %s)", err, output)
	}
	return &resp
}

func TestMCPServer_Initialize(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}`),
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.ProtocolVersion != ProtocolVersion {
		t.Errorf("expected protocolVersion=%s, got %s", ProtocolVersion, result.ProtocolVersion)
	}
	if result.Info.Name != "test-mcp" {
		t.Errorf("expected server name=test-mcp, got %s", result.Info.Name)
	}
	if result.Info.Version != "0.1.0" {
		t.Errorf("expected server version=0.1.0, got %s", result.Info.Version)
	}
	if result.Capabilities.Tools == nil {
		t.Error("expected tools capability to be advertised")
	}
}

func TestMCPServer_ToolsList_Empty(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(result.Tools))
	}
}

func TestMCPServer_ToolsList_WithRegisteredTool(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	srv.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echoes input back",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
		},
	}, func(_ context.Context, args json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: string(args)}},
		}, nil
	})

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`3`),
		Method:  "tools/list",
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "echo" {
		t.Errorf("expected tool name=echo, got %s", result.Tools[0].Name)
	}
}

func TestMCPServer_ToolsCall(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	srv.RegisterTool(Tool{
		Name:        "greet",
		Description: "Returns a greeting",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, args json.RawMessage) (*ToolCallResult, error) {
		var params struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, err
		}
		return &ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: "Hello, " + params.Name}},
		}, nil
	})

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"greet","arguments":{"name":"World"}}`),
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "Hello, World" {
		t.Errorf("expected text='Hello, World', got %q", result.Content[0].Text)
	}
}

func TestMCPServer_ToolsCall_UnknownTool(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"nonexistent"}`),
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != transport.CodeInvalidParams {
		t.Errorf("expected error code %d, got %d", transport.CodeInvalidParams, resp.Error.Code)
	}
}

func TestMCPServer_MethodNotFound(t *testing.T) {
	srv := New(nil, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`6`),
		Method:  "unknown/method",
	}

	resp := sendAndReceive(t, srv, request)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != transport.CodeMethodNotFound {
		t.Errorf("expected error code %d, got %d", transport.CodeMethodNotFound, resp.Error.Code)
	}
}

func TestMCPServer_Run_ContextCancellation(t *testing.T) {
	// Create a transport that blocks on read
	reader := &blockingReader{ch: make(chan struct{})}
	writer := &bytes.Buffer{}
	tr := transport.NewStdioTransport(reader, writer)

	srv := New(tr, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := srv.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error on context cancellation, got %v", err)
	}
}

type blockingReader struct {
	ch chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.ch
	return 0, nil
}

func TestMCPServer_Notification_DoesNotRespond(t *testing.T) {
	// Send a notification followed by a request, verify only request gets a response
	input := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"

	reader := strings.NewReader(input)
	writer := &bytes.Buffer{}
	tr := transport.NewStdioTransport(reader, writer)

	srv := New(tr, Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = srv.Run(ctx)

	// Should have exactly one response line (for the initialize request)
	output := strings.TrimSpace(writer.String())
	lines := strings.Split(output, "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 response line, got %d: %v", len(lines), lines)
	}

	var resp transport.JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}
