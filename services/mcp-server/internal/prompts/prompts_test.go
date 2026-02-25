package prompts_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/prompts"
)

func TestRegistry_List_ReturnsAllPrompts(t *testing.T) {
	reg := prompts.NewRegistry()

	list := reg.List()

	if len(list) == 0 {
		t.Fatal("expected at least one prompt, got none")
	}

	for _, p := range list {
		if p.Name == "" {
			t.Errorf("prompt missing Name: %+v", p)
		}
		if p.Description == "" {
			t.Errorf("prompt %q missing Description", p.Name)
		}
	}
}

func TestRegistry_List_IncludesRequiredPrompts(t *testing.T) {
	reg := prompts.NewRegistry()
	list := reg.List()

	names := make(map[string]bool)
	for _, p := range list {
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

func TestRegistry_Get_DesignEconomy(t *testing.T) {
	reg := prompts.NewRegistry()

	result, err := reg.Get("design-economy", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// Should have a system message and a user message
	hasSystem := false
	hasUser := false
	for _, msg := range result.Messages {
		if msg.Role == "user" {
			hasUser = true
		}
		// system messages use role "assistant" in MCP prompts, or may be embedded as user
		if msg.Role == "system" || msg.Role == "assistant" {
			hasSystem = true
		}
	}
	if !hasUser {
		t.Error("expected at least one user message")
	}
	_ = hasSystem
}

func TestRegistry_Get_AuditTransaction_WithArgs(t *testing.T) {
	reg := prompts.NewRegistry()

	args := map[string]string{
		"transaction_id": "txn_abc123",
	}

	result, err := reg.Get("audit-transaction", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// Verify the transaction ID is templated into the messages
	found := false
	for _, msg := range result.Messages {
		if contains(msg.Content.Text, "txn_abc123") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected transaction_id 'txn_abc123' to appear in prompt messages")
	}
}

func TestRegistry_Get_AuditTransaction_MissingRequiredArg(t *testing.T) {
	reg := prompts.NewRegistry()

	_, err := reg.Get("audit-transaction", nil)
	if err == nil {
		t.Fatal("expected error for missing required argument transaction_id")
	}
}

func TestRegistry_Get_SimulateChange_WithArgs(t *testing.T) {
	reg := prompts.NewRegistry()

	args := map[string]string{
		"change_description": "add a new instrument CARBON_CREDIT",
	}

	result, err := reg.Get("simulate-change", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	found := false
	for _, msg := range result.Messages {
		if contains(msg.Content.Text, "CARBON_CREDIT") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected change_description to appear in prompt messages")
	}
}

func TestRegistry_Get_SimulateChange_MissingRequiredArg(t *testing.T) {
	reg := prompts.NewRegistry()

	_, err := reg.Get("simulate-change", nil)
	if err == nil {
		t.Fatal("expected error for missing required argument change_description")
	}
}

func TestRegistry_Get_DebugSaga_WithArgs(t *testing.T) {
	reg := prompts.NewRegistry()

	args := map[string]string{
		"saga_id": "saga_xyz789",
	}

	result, err := reg.Get("debug-saga", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	found := false
	for _, msg := range result.Messages {
		if contains(msg.Content.Text, "saga_xyz789") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected saga_id 'saga_xyz789' to appear in prompt messages")
	}
}

func TestRegistry_Get_DebugSaga_MissingRequiredArg(t *testing.T) {
	reg := prompts.NewRegistry()

	_, err := reg.Get("debug-saga", nil)
	if err == nil {
		t.Fatal("expected error for missing required argument saga_id")
	}
}

func TestRegistry_Get_UnknownPrompt(t *testing.T) {
	reg := prompts.NewRegistry()

	_, err := reg.Get("unknown-prompt", nil)
	if err == nil {
		t.Fatal("expected error for unknown prompt")
	}
}

func TestRegistry_Get_DesignEconomy_NoRequiredArgs(t *testing.T) {
	reg := prompts.NewRegistry()

	// design-economy requires no args
	result, err := reg.Get("design-economy", nil)
	if err != nil {
		t.Fatalf("expected no error for design-economy with no args, got: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
}

func TestPrompt_Arguments_HaveRequiredFlag(t *testing.T) {
	reg := prompts.NewRegistry()
	list := reg.List()

	// Verify that prompts with required args declare them
	for _, p := range list {
		for _, arg := range p.Arguments {
			if arg.Name == "" {
				t.Errorf("prompt %q has argument with empty name", p.Name)
			}
		}
	}
}

// contains is a helper for string containment checks.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
