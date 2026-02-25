package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/mcp-server/internal/prompts"
	"github.com/meridianhub/meridian/services/mcp-server/internal/resources"
	"github.com/meridianhub/meridian/services/mcp-server/internal/transport"
)

// sendAndReceiveWithResourcesPrompts creates a server with resource and prompt providers
// and sends one request, returning the parsed response.
func sendAndReceiveWithResourcesPrompts(t *testing.T, resourceProvider *resources.Provider, promptRegistry *prompts.Registry, request *transport.JSONRPCMessage) *transport.JSONRPCMessage {
	t.Helper()

	reqData, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reqData = append(reqData, '\n')

	reader := bytes.NewReader(reqData)
	writer := &bytes.Buffer{}
	tr := transport.NewStdioTransport(reader, writer)

	cfg := Config{ServerName: "test-mcp", ServerVersion: "0.1.0"}
	srv := New(tr, cfg, testLogger())
	srv.SetResourceProvider(resourceProvider)
	srv.SetPromptRegistry(promptRegistry)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = srv.Run(ctx)

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

func TestMCPServer_ResourcesList(t *testing.T) {
	provider := resources.New(nil)

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`10`),
		Method:  "resources/list",
	}

	resp := sendAndReceiveWithResourcesPrompts(t, provider, nil, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result ResourcesListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Resources) == 0 {
		t.Error("expected at least one resource")
	}

	// Check that each resource has required fields
	for _, r := range result.Resources {
		if r.URI == "" {
			t.Errorf("resource missing URI: %+v", r)
		}
		if r.Name == "" {
			t.Errorf("resource %q missing name", r.URI)
		}
	}
}

func TestMCPServer_ResourcesRead_StarlarkGuide(t *testing.T) {
	provider := resources.New(nil)

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`11`),
		Method:  "resources/read",
		Params:  json.RawMessage(`{"uri":"meridian://docs/starlark-guide"}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, provider, nil, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result ResourceReadResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content block")
	}
	if !strings.Contains(result.Contents[0].Text, "Starlark") {
		t.Errorf("expected Starlark guide content in response")
	}
}

func TestMCPServer_ResourcesRead_UnknownURI(t *testing.T) {
	provider := resources.New(nil)

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`12`),
		Method:  "resources/read",
		Params:  json.RawMessage(`{"uri":"meridian://unknown/thing"}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, provider, nil, request)

	if resp.Error == nil {
		t.Fatal("expected error for unknown resource URI")
	}
}

func TestMCPServer_ResourcesRead_MissingURI(t *testing.T) {
	provider := resources.New(nil)

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`13`),
		Method:  "resources/read",
		Params:  json.RawMessage(`{}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, provider, nil, request)

	if resp.Error == nil {
		t.Fatal("expected error for missing URI")
	}
}

func TestMCPServer_PromptsList(t *testing.T) {
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`20`),
		Method:  "prompts/list",
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, reg, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result PromptsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Prompts) == 0 {
		t.Error("expected at least one prompt")
	}
}

func TestMCPServer_PromptsGet_DesignEconomy(t *testing.T) {
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`21`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{"name":"design-economy"}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, reg, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result PromptGetResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Error("expected at least one message")
	}
}

func TestMCPServer_PromptsGet_AuditTransaction(t *testing.T) {
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`22`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{"name":"audit-transaction","arguments":{"transaction_id":"txn_test_001"}}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, reg, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result PromptGetResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// Check transaction ID appears in messages
	found := false
	for _, msg := range result.Messages {
		if strings.Contains(msg.Content.Text, "txn_test_001") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected transaction_id to appear in prompt messages")
	}
}

func TestMCPServer_PromptsGet_UnknownPrompt(t *testing.T) {
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`23`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{"name":"unknown-prompt"}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, reg, request)

	if resp.Error == nil {
		t.Fatal("expected error for unknown prompt")
	}
}

func TestMCPServer_PromptsGet_MissingName(t *testing.T) {
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`24`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, reg, request)

	if resp.Error == nil {
		t.Fatal("expected error for missing prompt name")
	}
}

func TestMCPServer_Initialize_AdvertisesResourcesAndPrompts(t *testing.T) {
	provider := resources.New(nil)
	reg := prompts.NewRegistry()

	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`30`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}`),
	}

	resp := sendAndReceiveWithResourcesPrompts(t, provider, reg, request)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if result.Capabilities.Resources == nil {
		t.Error("expected resources capability to be advertised")
	}
	if result.Capabilities.Prompts == nil {
		t.Error("expected prompts capability to be advertised")
	}
}

func TestMCPServer_ResourcesList_NoProvider_MethodNotFound(t *testing.T) {
	// Server without resource provider - should return method not found
	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`40`),
		Method:  "resources/list",
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, nil, request)

	if resp.Error == nil {
		t.Fatal("expected error when no resource provider configured")
	}
}

func TestMCPServer_PromptsList_NoRegistry_MethodNotFound(t *testing.T) {
	// Server without prompt registry - should return method not found
	request := &transport.JSONRPCMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      json.RawMessage(`41`),
		Method:  "prompts/list",
	}

	resp := sendAndReceiveWithResourcesPrompts(t, nil, nil, request)

	if resp.Error == nil {
		t.Fatal("expected error when no prompt registry configured")
	}
}
