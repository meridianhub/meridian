package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
)

func TestRegistry_Register_ValidSchema(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name:        "test.read",
		Description: "A test read tool",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"id"},
		},
		Category: tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "ok", nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestRegistry_Register_EmptyName(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name:        "",
		InputSchema: map[string]interface{}{"type": "object"},
		Category:    tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, nil
		},
	}

	err := r.Register(tool)
	if err == nil {
		t.Fatal("expected error for empty tool name, got nil")
	}
	if !errors.Is(err, tools.ErrToolNameRequired) {
		t.Errorf("expected ErrToolNameRequired, got %v", err)
	}
}

func TestRegistry_Register_NilHandler(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name:        "no.handler",
		InputSchema: map[string]interface{}{"type": "object"},
		Category:    tools.CategoryRead,
		Handler:     nil,
	}

	err := r.Register(tool)
	if err == nil {
		t.Fatal("expected error for nil handler, got nil")
	}
	if !errors.Is(err, tools.ErrToolHandlerRequired) {
		t.Errorf("expected ErrToolHandlerRequired, got %v", err)
	}
}

func TestRegistry_Register_SchemaDeepCopy(t *testing.T) {
	r := tools.NewRegistry()
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{"type": "string"},
		},
	}
	tool := tools.Tool{
		Name:        "isolated.tool",
		InputSchema: schema,
		Category:    tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Mutate the original schema after registration — should not affect registry.
	schema["type"] = "string"

	listed := r.List()
	if len(listed) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(listed))
	}
	if listed[0].InputSchema["type"] != "object" {
		t.Errorf("registry schema was mutated by external change: got type=%v", listed[0].InputSchema["type"])
	}
}

func TestRegistry_Call_NilParamsNormalized(t *testing.T) {
	r := tools.NewRegistry()
	var receivedParams json.RawMessage
	tool := tools.Tool{
		Name:        "empty.params",
		InputSchema: map[string]interface{}{"type": "object"},
		Category:    tools.CategoryRead,
		Handler: func(_ context.Context, params json.RawMessage) (interface{}, error) {
			receivedParams = params
			return nil, nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Call with nil params — handler should receive "{}" not nil.
	if _, err := r.Call(context.Background(), "empty.params", nil); err != nil {
		t.Fatalf("call: %v", err)
	}

	var v map[string]interface{}
	if err := json.Unmarshal(receivedParams, &v); err != nil {
		t.Errorf("handler received non-unmarshalable params: %v (got %q)", err, string(receivedParams))
	}
}

func TestRegistry_Register_InvalidSchema(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name: "bad.tool",
		InputSchema: map[string]interface{}{
			"type": "invalid-type-value",
		},
		Category: tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return nil, nil
		},
	}

	if err := r.Register(tool); err == nil {
		t.Fatal("expected error for invalid schema, got nil")
	}
}

func TestRegistry_Call_ValidInput(t *testing.T) {
	r := tools.NewRegistry()
	called := false
	tool := tools.Tool{
		Name: "account.get",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"accountId": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"accountId"},
		},
		Category: tools.CategoryRead,
		Handler: func(_ context.Context, params json.RawMessage) (interface{}, error) {
			called = true
			var p map[string]string
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			return map[string]string{"id": p["accountId"]}, nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	params := json.RawMessage(`{"accountId": "acc-123"}`)
	result, err := r.Call(context.Background(), "account.get", params)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !called {
		t.Fatal("expected handler to be called")
	}
	_ = result
}

func TestRegistry_Call_MissingRequiredField(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name: "account.get",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"accountId": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"accountId"},
		},
		Category: tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			t.Fatal("handler should not be called for invalid input")
			return nil, nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Missing required "accountId" field
	params := json.RawMessage(`{}`)
	_, err := r.Call(context.Background(), "account.get", params)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}

func TestRegistry_Call_WrongType(t *testing.T) {
	r := tools.NewRegistry()
	tool := tools.Tool{
		Name: "account.get",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount": map[string]interface{}{"type": "number"},
			},
			"required": []interface{}{"amount"},
		},
		Category: tools.CategorySimulate,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			t.Fatal("handler should not be called for invalid input")
			return nil, nil
		},
	}

	if err := r.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Wrong type: string instead of number
	params := json.RawMessage(`{"amount": "not-a-number"}`)
	_, err := r.Call(context.Background(), "account.get", params)
	if err == nil {
		t.Fatal("expected validation error for wrong type")
	}
}

func TestRegistry_Call_UnknownTool(t *testing.T) {
	r := tools.NewRegistry()

	_, err := r.Call(context.Background(), "nonexistent.tool", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !errors.Is(err, tools.ErrToolNotFound) {
		t.Errorf("expected ErrToolNotFound, got %v", err)
	}
}

func TestRegistry_List_ReturnsToolMetadata(t *testing.T) {
	r := tools.NewRegistry()

	tools1 := []tools.Tool{
		{
			Name:        "alpha.read",
			Description: "Alpha read",
			InputSchema: map[string]interface{}{"type": "object"},
			Category:    tools.CategoryRead,
			Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
				return nil, nil
			},
		},
		{
			Name:        "beta.write",
			Description: "Beta write",
			InputSchema: map[string]interface{}{"type": "object"},
			Category:    tools.CategoryWrite,
			Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
				return nil, nil
			},
		},
	}

	for _, tool := range tools1 {
		if err := r.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.Name, err)
		}
	}

	listed := r.List()
	if len(listed) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(listed))
	}

	// Verify handler is not exposed in listing (Tool metadata only)
	nameSet := map[string]bool{}
	for _, tool := range listed {
		nameSet[tool.Name] = true
		if tool.Handler != nil {
			t.Errorf("tool %s should not expose Handler in listing", tool.Name)
		}
	}

	if !nameSet["alpha.read"] || !nameSet["beta.write"] {
		t.Errorf("expected both tools in list, got names: %v", nameSet)
	}
}

func TestRegistry_Concurrent_RegisterAndCall(t *testing.T) {
	r := tools.NewRegistry()

	// Pre-register a tool that will be called concurrently
	baseTool := tools.Tool{
		Name: "concurrent.read",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
		Category: tools.CategoryRead,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "result", nil
		},
	}
	if err := r.Register(baseTool); err != nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent calls
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Call(context.Background(), "concurrent.read", json.RawMessage(`{}`))
			if err != nil {
				errors <- err
			}
		}()
	}

	// Concurrent registrations of new tools
	for i := 0; i < 10; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			newTool := tools.Tool{
				Name:        "dynamic.tool." + string(rune('a'+idx)),
				Description: "Dynamic tool",
				InputSchema: map[string]interface{}{"type": "object"},
				Category:    tools.CategoryRead,
				Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
					return nil, nil
				},
			}
			if err := r.Register(newTool); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent error: %v", err)
	}
}
