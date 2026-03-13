package prompts_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/services/mcp-server/internal/prompts"
)

// setupClientServer creates an in-memory MCP server+client pair with all
// prompts registered, returning the connected client session. The caller must
// defer cleanup.
func setupClientServer(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	prompts.RegisterPrompts(srv)

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs
}

func TestRegisterPrompts_ListReturnsAllPrompts(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	if len(result.Prompts) == 0 {
		t.Fatal("expected at least one prompt, got none")
	}

	for _, p := range result.Prompts {
		if p.Name == "" {
			t.Errorf("prompt missing Name: %+v", p)
		}
		if p.Description == "" {
			t.Errorf("prompt %q missing Description", p.Name)
		}
	}
}

func TestRegisterPrompts_IncludesRequiredPrompts(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}

	names := make(map[string]bool)
	for _, p := range result.Prompts {
		names[p.Name] = true
	}

	required := []string{
		"design-economy",
		"audit-transaction",
		"simulate-change",
		"debug-saga",
	}
	for _, name := range required {
		if !names[name] {
			t.Errorf("expected prompt %q in list, not found", name)
		}
	}
}

func TestGetPrompt_DesignEconomy(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "design-economy",
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	hasUser := false
	for _, msg := range result.Messages {
		if msg.Role == "user" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Error("expected at least one user message")
	}
}

func TestGetPrompt_AuditTransaction_WithArgs(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "audit-transaction",
		Arguments: map[string]string{
			"transaction_id": "txn_abc123",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	found := false
	for _, msg := range result.Messages {
		tc, ok := msg.Content.(*mcp.TextContent)
		if ok && strings.Contains(tc.Text, "txn_abc123") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected transaction_id 'txn_abc123' to appear in prompt messages")
	}
}

func TestGetPrompt_AuditTransaction_MissingRequiredArg(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "audit-transaction",
	})
	if err == nil {
		t.Fatal("expected error for missing required argument transaction_id")
	}
}

func TestGetPrompt_SimulateChange_WithArgs(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "simulate-change",
		Arguments: map[string]string{
			"change_description": "add a new instrument CARBON_CREDIT",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	found := false
	for _, msg := range result.Messages {
		tc, ok := msg.Content.(*mcp.TextContent)
		if ok && strings.Contains(tc.Text, "CARBON_CREDIT") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected change_description to appear in prompt messages")
	}
}

func TestGetPrompt_SimulateChange_MissingRequiredArg(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "simulate-change",
	})
	if err == nil {
		t.Fatal("expected error for missing required argument change_description")
	}
}

func TestGetPrompt_DebugSaga_WithArgs(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "debug-saga",
		Arguments: map[string]string{
			"saga_id": "saga_xyz789",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	found := false
	for _, msg := range result.Messages {
		tc, ok := msg.Content.(*mcp.TextContent)
		if ok && strings.Contains(tc.Text, "saga_xyz789") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected saga_id 'saga_xyz789' to appear in prompt messages")
	}
}

func TestGetPrompt_DebugSaga_MissingRequiredArg(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "debug-saga",
	})
	if err == nil {
		t.Fatal("expected error for missing required argument saga_id")
	}
}

func TestGetPrompt_UnknownPrompt(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "unknown-prompt",
	})
	if err == nil {
		t.Fatal("expected error for unknown prompt")
	}
}

func TestGetPrompt_DesignEconomy_NoRequiredArgs(t *testing.T) {
	cs := setupClientServer(t)
	ctx := context.Background()

	result, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "design-economy",
	})
	if err != nil {
		t.Fatalf("expected no error for design-economy with no args, got: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}
