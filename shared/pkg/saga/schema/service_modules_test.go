package schema

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// TestParseHandlerTree tests the tree building from handler names.
func TestParseHandlerTree(t *testing.T) {
	tests := []struct {
		name       string
		handlers   []string
		wantNodes  []string // Expected top-level service names
		wantLeaves map[string][]string
	}{
		{
			name:       "empty",
			handlers:   []string{},
			wantNodes:  []string{},
			wantLeaves: map[string][]string{},
		},
		{
			name:       "single 2-part handler",
			handlers:   []string{"repository.save"},
			wantNodes:  []string{"repository"},
			wantLeaves: map[string][]string{"repository": {"save"}},
		},
		{
			name: "multiple 2-part handlers same service",
			handlers: []string{
				"notification.send",
				"notification.cancel",
			},
			wantNodes:  []string{"notification"},
			wantLeaves: map[string][]string{"notification": {"cancel", "send"}},
		},
		{
			name: "3-part handlers",
			handlers: []string{
				"current_account.position_keeping.initiate_log",
				"current_account.position_keeping.cancel_log",
			},
			wantNodes: []string{"current_account"},
			// position_keeping is a branch, initiate_log and cancel_log are leaves
			wantLeaves: map[string][]string{"current_account.position_keeping": {"cancel_log", "initiate_log"}},
		},
		{
			name: "mixed 2 and 3 part handlers",
			handlers: []string{
				"repository.save",
				"position_keeping.initiate_log",
				"position_keeping.cancel_log",
			},
			wantNodes: []string{"position_keeping", "repository"},
			wantLeaves: map[string][]string{
				"repository":       {"save"},
				"position_keeping": {"cancel_log", "initiate_log"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree := parseHandlerTree(tt.handlers)

			// Check top-level nodes
			var topLevelNames []string
			for name := range tree.children {
				topLevelNames = append(topLevelNames, name)
			}
			assert.ElementsMatch(t, tt.wantNodes, topLevelNames)

			// Check leaves at each path
			for path, wantLeaves := range tt.wantLeaves {
				node := tree.findNode(path)
				require.NotNil(t, node, "node at path %q should exist", path)

				var leafNames []string
				for name := range node.handlers {
					leafNames = append(leafNames, name)
				}
				assert.ElementsMatch(t, wantLeaves, leafNames, "leaves at %q", path)
			}
		})
	}
}

// TestParseHandlerTree_ConflictDetection tests that conflicts are detected.
func TestParseHandlerTree_ConflictDetection(t *testing.T) {
	// This tests that a name cannot be both a branch and a leaf.
	// For example: "foo.bar" and "foo.bar.baz" would conflict because
	// "bar" would need to be both a handler and a namespace.

	handlers := []string{
		"foo.bar",     // "bar" is a handler under "foo"
		"foo.bar.baz", // "bar" would need to be a namespace containing "baz"
	}

	tree := parseHandlerTree(handlers)
	err := tree.validate()
	assert.Error(t, err, "should detect naming conflict")
	assert.Contains(t, err.Error(), "conflict")
}

// TestBuildServiceModules tests the module building from handler registry and schema.
func TestBuildServiceModules(t *testing.T) {
	// Create a minimal handler registry
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"log_id": "test-123", "status": "INITIATED"}, nil
	})
	_ = registry.Register("repository.save", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"entity_id": "saved-456", "status": "SAVED"}, nil
	})

	// Create schema from YAML
	testSchema, err := Parse([]byte(`
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Test handler"
    compensation_strategy: none
    params:
      position_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
      status:
        type: string
  repository.save:
    description: "Save entity"
    compensation_strategy: none
    params:
      entity_type:
        type: string
        required: true
      entity:
        type: map
        required: true
    returns:
      entity_id:
        type: string
      status:
        type: string
`))
	require.NoError(t, err)

	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	// Should have top-level modules
	assert.Contains(t, modules, "position_keeping")
	assert.Contains(t, modules, "repository")

	// position_keeping should be a struct
	pkModule, ok := modules["position_keeping"].(*starlarkstruct.Struct)
	require.True(t, ok, "position_keeping should be a struct")

	// initiate_log should be a builtin function
	initLogVal, err := pkModule.Attr("initiate_log")
	require.NoError(t, err)
	_, ok = initLogVal.(*starlark.Builtin)
	assert.True(t, ok, "initiate_log should be a builtin")

	// repository.save should be accessible
	repoModule, ok := modules["repository"].(*starlarkstruct.Struct)
	require.True(t, ok, "repository should be a struct")

	saveVal, err := repoModule.Attr("save")
	require.NoError(t, err)
	_, ok = saveVal.(*starlark.Builtin)
	assert.True(t, ok, "save should be a builtin")
}

// TestBuildServiceModules_NestedModules tests 3-level nested modules.
func TestBuildServiceModules_NestedModules(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("current_account.position_keeping.initiate_log", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"log_id": "test-123"}, nil
	})
	_ = registry.Register("current_account.position_keeping.cancel_log", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"log_id": "test-123"}, nil
	})

	testSchema, err := Parse([]byte(`
service: current_account
version: "1.0"
handlers:
  current_account.position_keeping.initiate_log:
    description: "Initiate log"
    compensation_strategy: none
    params:
      position_id:
        type: string
        required: true
    returns:
      log_id:
        type: string
  current_account.position_keeping.cancel_log:
    description: "Cancel log"
    compensation_strategy: none
    params:
      log_id:
        type: string
        required: true
    returns:
      log_id:
        type: string
`))
	require.NoError(t, err)

	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	// Should have current_account at top level
	assert.Contains(t, modules, "current_account")

	caModule, ok := modules["current_account"].(*starlarkstruct.Struct)
	require.True(t, ok, "current_account should be a struct")

	// position_keeping should be a nested struct
	pkVal, err := caModule.Attr("position_keeping")
	require.NoError(t, err)
	pkModule, ok := pkVal.(*starlarkstruct.Struct)
	require.True(t, ok, "position_keeping should be a struct")

	// initiate_log should be a builtin
	initLogVal, err := pkModule.Attr("initiate_log")
	require.NoError(t, err)
	_, ok = initLogVal.(*starlark.Builtin)
	assert.True(t, ok, "initiate_log should be a builtin")
}

// TestWrapHandler_ParameterValidation tests that wrapHandler validates params against schema.
func TestWrapHandler_ParameterValidation(t *testing.T) {
	handlerCalled := false
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		handlerCalled = true
		return map[string]any{"result": "success"}, nil
	}

	handlerDef := &HandlerDef{
		Description: "Test handler",
		Params: map[string]*FieldDef{
			"required_param": {Type: TypeString, Required: true},
			"optional_param": {Type: TypeString, Required: false},
			"direction":      {Type: TypeEnum, Required: true, Values: []string{"DEBIT", "CREDIT"}},
		},
		Returns: map[string]*FieldDef{
			"result": {Type: TypeString},
		},
	}

	// Create a StarlarkContext
	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)

	t.Run("valid params", func(t *testing.T) {
		handlerCalled = false
		thread := &starlark.Thread{Name: "test"}
		setStarlarkContext(thread, starlarkCtx)

		kwargs := []starlark.Tuple{
			{starlark.String("required_param"), starlark.String("value")},
			{starlark.String("direction"), starlark.String("DEBIT")},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.NoError(t, err)
		assert.True(t, handlerCalled, "handler should have been called")
	})

	t.Run("missing required param", func(t *testing.T) {
		handlerCalled = false
		thread := &starlark.Thread{Name: "test"}
		setStarlarkContext(thread, starlarkCtx)

		kwargs := []starlark.Tuple{
			{starlark.String("direction"), starlark.String("DEBIT")},
			// missing required_param
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "required_param")
		assert.False(t, handlerCalled, "handler should not have been called")
	})

	t.Run("invalid enum value", func(t *testing.T) {
		handlerCalled = false
		thread := &starlark.Thread{Name: "test"}
		setStarlarkContext(thread, starlarkCtx)

		kwargs := []starlark.Tuple{
			{starlark.String("required_param"), starlark.String("value")},
			{starlark.String("direction"), starlark.String("INVALID")},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "INVALID")
		assert.False(t, handlerCalled, "handler should not have been called")
	})
}

// TestStarlarkContextThreading tests that StarlarkContext is properly passed via thread-local.
func TestStarlarkContextThreading(t *testing.T) {
	sagaExecID := uuid.New()
	correlationID := uuid.New()

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: sagaExecID,
		CorrelationID:   correlationID,
		KnowledgeAt:     time.Now(),
		Logger:          slog.Default(),
	}

	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	// Should be able to retrieve it
	retrieved := getStarlarkContext(thread)
	require.NotNil(t, retrieved)
	assert.Equal(t, sagaExecID, retrieved.SagaExecutionID)
	assert.Equal(t, correlationID, retrieved.CorrelationID)
}

// TestStarlarkContextThreading_NilContext tests behavior when no context is set.
func TestStarlarkContextThreading_NilContext(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}

	retrieved := getStarlarkContext(thread)
	assert.Nil(t, retrieved)
}

// TestWrapHandler_DecimalConversion tests Decimal type conversion from Starlark.
func TestWrapHandler_DecimalConversion(t *testing.T) {
	var receivedAmount decimal.Decimal
	handler := func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		if amt, ok := params["amount"]; ok {
			receivedAmount = amt.(decimal.Decimal)
		}
		return map[string]any{"result": "success"}, nil
	}

	handlerDef := &HandlerDef{
		Params: map[string]*FieldDef{
			"amount": {Type: TypeDecimal, Required: true},
		},
		Returns: map[string]*FieldDef{
			"result": {Type: TypeString},
		},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	t.Run("string to Decimal", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("amount"), starlark.String("123.45")},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.NoError(t, err)
		assert.Equal(t, "123.45", receivedAmount.String())
	})

	t.Run("int to Decimal", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("amount"), starlark.MakeInt(100)},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.NoError(t, err)
		assert.Equal(t, "100", receivedAmount.String())
	})

	t.Run("float to Decimal", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("amount"), starlark.Float(99.99)},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.NoError(t, err)
		// Float conversion might have precision differences
		assert.True(t, receivedAmount.GreaterThan(decimal.NewFromFloat(99.98)))
		assert.True(t, receivedAmount.LessThan(decimal.NewFromFloat(100)))
	})
}

// TestWrapHandler_ReturnValueConversion tests that Go return values are converted to Starlark.
func TestWrapHandler_ReturnValueConversion(t *testing.T) {
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{
			"string_val":  "hello",
			"int_val":     int64(42),
			"decimal_val": decimal.NewFromFloat(123.45),
			"bool_val":    true,
		}, nil
	}

	handlerDef := &HandlerDef{
		Params:  map[string]*FieldDef{},
		Returns: map[string]*FieldDef{},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	result, err := builtin.CallInternal(thread, nil, nil)
	require.NoError(t, err)

	// Result should be a struct
	resultStruct, ok := result.(*starlarkstruct.Struct)
	require.True(t, ok, "result should be a struct, got %T", result)

	// Check values
	strVal, err := resultStruct.Attr("string_val")
	require.NoError(t, err)
	assert.Equal(t, starlark.String("hello"), strVal)

	intVal, err := resultStruct.Attr("int_val")
	require.NoError(t, err)
	assert.Equal(t, starlark.MakeInt64(42), intVal)

	boolVal, err := resultStruct.Attr("bool_val")
	require.NoError(t, err)
	assert.Equal(t, starlark.Bool(true), boolVal)
}

// TestIntegration_StarlarkExecution tests end-to-end execution with a real Starlark script.
func TestIntegration_StarlarkExecution(t *testing.T) {
	// Set up handler registry
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return map[string]any{
			"log_id":   ctx.NewUUID(ctx.SagaExecutionID, "position_log").String(),
			"status":   "INITIATED",
			"position": params["position_id"],
		}, nil
	})

	// Set up schema
	testSchema, err := Parse([]byte(`
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate position log"
    compensation_strategy: none
    params:
      position_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
      status:
        type: string
      position:
        type: string
`))
	require.NoError(t, err)

	// Build service modules
	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	// Create Starlark context
	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		Logger:          slog.Default(),
	}

	// Create thread with context
	thread := &starlark.Thread{
		Name: "test_saga",
	}
	setStarlarkContext(thread, starlarkCtx)

	// Build predeclared with our service modules
	predeclared := starlark.StringDict{
		"True":  starlark.True,
		"False": starlark.False,
		"None":  starlark.None,
	}
	for name, module := range modules {
		predeclared[name] = module
	}

	// Execute a simple Starlark script that calls our handler
	script := `
result = position_keeping.initiate_log(
    position_id="pos-123",
    amount="100.00",
    direction="DEBIT"
)
output_log_id = result.log_id
output_status = result.status
`

	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "test.star", script, predeclared)
	require.NoError(t, err)

	// Check outputs
	logID, ok := globals["output_log_id"]
	require.True(t, ok, "output_log_id should be in globals")
	assert.NotEmpty(t, logID.String())

	status, ok := globals["output_status"]
	require.True(t, ok, "output_status should be in globals")
	assert.Equal(t, `"INITIATED"`, status.String())
}

// TestHandlerTree_FindNode tests the findNode helper.
func TestHandlerTree_FindNode(t *testing.T) {
	tree := parseHandlerTree([]string{
		"service.domain.action",
		"service.domain.other_action",
		"simple.handler",
	})

	t.Run("finds top-level node", func(t *testing.T) {
		node := tree.findNode("service")
		require.NotNil(t, node)
	})

	t.Run("finds nested node", func(t *testing.T) {
		node := tree.findNode("service.domain")
		require.NotNil(t, node)
		assert.Contains(t, node.handlers, "action")
		assert.Contains(t, node.handlers, "other_action")
	})

	t.Run("returns nil for non-existent", func(t *testing.T) {
		node := tree.findNode("nonexistent")
		assert.Nil(t, node)
	})

	t.Run("returns nil for non-existent nested", func(t *testing.T) {
		node := tree.findNode("service.nonexistent")
		assert.Nil(t, node)
	})
}

// TestBuildServiceModules_EmptyRegistry tests that an empty registry produces empty modules.
func TestBuildServiceModules_EmptyRegistry(t *testing.T) {
	registry := saga.NewHandlerRegistry()

	modules, err := BuildServiceModules(registry)
	require.NoError(t, err)
	assert.Empty(t, modules)
}

// TestBuildServiceModules_DeriveFromRegistry tests that BuildServiceModules derives schema
// from handler metadata and produces working modules.
func TestBuildServiceModules_DeriveFromRegistry(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("test.handler", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"status": "ok"}, nil
	})

	// Handler without metadata gets empty params — should still build
	modules, err := BuildServiceModules(registry)
	require.NoError(t, err)
	assert.Contains(t, modules, "test")
}

// TestWrapHandler_PositionalArgsRejected tests that positional args are rejected.
func TestWrapHandler_PositionalArgsRejected(t *testing.T) {
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	handlerDef := &HandlerDef{
		Params:  map[string]*FieldDef{},
		Returns: map[string]*FieldDef{},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	// Try calling with positional arguments
	args := starlark.Tuple{starlark.String("value")}
	_, err := builtin.CallInternal(thread, args, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positional")
}

// TestWrapHandler_MissingContext tests behavior when context is not set.
func TestWrapHandler_MissingContext(t *testing.T) {
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	handlerDef := &HandlerDef{
		Params:  map[string]*FieldDef{},
		Returns: map[string]*FieldDef{},
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	// Not setting StarlarkContext

	_, err := builtin.CallInternal(thread, nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStarlarkContext)
}

// TestWrapHandler_HandlerError tests that handler errors are propagated.
func TestWrapHandler_HandlerError(t *testing.T) {
	expectedErr := errors.New("handler failed")
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, expectedErr
	}

	handlerDef := &HandlerDef{
		Params:  map[string]*FieldDef{},
		Returns: map[string]*FieldDef{},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	_, err := builtin.CallInternal(thread, nil, nil)
	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

// TestWrapHandler_NilReturn tests that nil return is handled correctly.
func TestWrapHandler_NilReturn(t *testing.T) {
	handler := func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	handlerDef := &HandlerDef{
		Params:  map[string]*FieldDef{},
		Returns: map[string]*FieldDef{},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	result, err := builtin.CallInternal(thread, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.None, result)
}

// TestWrapHandler_ArrayParameter tests array parameter handling.
func TestWrapHandler_ArrayParameter(t *testing.T) {
	var receivedEntries []any
	handler := func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		if entries, ok := params["entries"]; ok {
			receivedEntries = entries.([]any)
		}
		return map[string]any{"count": len(receivedEntries)}, nil
	}

	handlerDef := &HandlerDef{
		Params: map[string]*FieldDef{
			"entries": {Type: TypeArray, Required: true},
		},
		Returns: map[string]*FieldDef{
			"count": {Type: TypeInt32},
		},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	// Create a Starlark list
	entriesList := starlark.NewList([]starlark.Value{
		starlark.String("entry1"),
		starlark.String("entry2"),
		starlark.String("entry3"),
	})

	kwargs := []starlark.Tuple{
		{starlark.String("entries"), entriesList},
	}
	_, err := builtin.CallInternal(thread, nil, kwargs)
	require.NoError(t, err)
	assert.Len(t, receivedEntries, 3)
}

// TestWrapHandler_MapParameter tests map parameter handling.
func TestWrapHandler_MapParameter(t *testing.T) {
	var receivedEntity map[string]any
	handler := func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		if entity, ok := params["entity"]; ok {
			receivedEntity = entity.(map[string]any)
		}
		return map[string]any{"status": "saved"}, nil
	}

	handlerDef := &HandlerDef{
		Params: map[string]*FieldDef{
			"entity": {Type: TypeMap, Required: true},
		},
		Returns: map[string]*FieldDef{
			"status": {Type: TypeString},
		},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	// Create a Starlark dict
	entityDict := starlark.NewDict(2)
	_ = entityDict.SetKey(starlark.String("id"), starlark.String("123"))
	_ = entityDict.SetKey(starlark.String("name"), starlark.String("Test"))

	kwargs := []starlark.Tuple{
		{starlark.String("entity"), entityDict},
	}
	_, err := builtin.CallInternal(thread, nil, kwargs)
	require.NoError(t, err)
	assert.Equal(t, "123", receivedEntity["id"])
	assert.Equal(t, "Test", receivedEntity["name"])
}

// TestWrapHandler_IntegerCoercion tests that integer types are coerced through wrapHandler.
func TestWrapHandler_IntegerCoercion(t *testing.T) {
	var receivedCount int32
	var receivedVersion uint32
	var receivedTimestamp int64
	handler := func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		if v, ok := params["count"]; ok {
			receivedCount = v.(int32)
		}
		if v, ok := params["version"]; ok {
			receivedVersion = v.(uint32)
		}
		if v, ok := params["timestamp"]; ok {
			receivedTimestamp = v.(int64)
		}
		return map[string]any{"status": "ok"}, nil
	}

	handlerDef := &HandlerDef{
		Params: map[string]*FieldDef{
			"count":     {Type: TypeInt32, Required: true},
			"version":   {Type: TypeUint32, Required: true},
			"timestamp": {Type: TypeInt64, Required: true},
		},
		Returns: map[string]*FieldDef{
			"status": {Type: TypeString},
		},
	}

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	builtin := wrapHandler("test.handler", handler, handlerDef)
	thread := &starlark.Thread{Name: "test"}
	setStarlarkContext(thread, starlarkCtx)

	t.Run("int32 coercion from Starlark int", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("count"), starlark.MakeInt(42)},
			{starlark.String("version"), starlark.MakeUint(100)},
			{starlark.String("timestamp"), starlark.MakeInt64(1706000000)},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.NoError(t, err)
		assert.Equal(t, int32(42), receivedCount)
		assert.Equal(t, uint32(100), receivedVersion)
		assert.Equal(t, int64(1706000000), receivedTimestamp)
	})

	t.Run("int32 overflow rejected", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("count"), starlark.MakeInt64(3000000000)}, // > MaxInt32
			{starlark.String("version"), starlark.MakeUint(1)},
			{starlark.String("timestamp"), starlark.MakeInt64(1)},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "int32")
		assert.Contains(t, err.Error(), "count")
	})

	t.Run("uint32 negative rejected", func(t *testing.T) {
		kwargs := []starlark.Tuple{
			{starlark.String("count"), starlark.MakeInt(1)},
			{starlark.String("version"), starlark.MakeInt(-1)}, // negative for uint32
			{starlark.String("timestamp"), starlark.MakeInt64(1)},
		}
		_, err := builtin.CallInternal(thread, nil, kwargs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uint32")
		assert.Contains(t, err.Error(), "version")
	})
}

// TestIntegration_NumericCoercion tests end-to-end numeric coercion via Starlark script.
func TestIntegration_NumericCoercion(t *testing.T) {
	var receivedCount int32
	var receivedVersion uint32

	registry := saga.NewHandlerRegistry()
	_ = registry.Register("test_service.process", func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		receivedCount = params["count"].(int32)
		receivedVersion = params["version"].(uint32)
		return map[string]any{"processed": true}, nil
	})

	testSchema, err := Parse([]byte(`
service: test
version: "1.0"
handlers:
  test_service.process:
    description: "Process with numeric types"
    compensation_strategy: none
    params:
      count:
        type: int32
        required: true
      version:
        type: uint32
        required: true
    returns:
      processed:
        type: bool
`))
	require.NoError(t, err)

	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		Logger:          slog.Default(),
	}

	thread := &starlark.Thread{Name: "test_saga"}
	setStarlarkContext(thread, starlarkCtx)

	predeclared := starlark.StringDict{
		"True":  starlark.True,
		"False": starlark.False,
		"None":  starlark.None,
	}
	for name, module := range modules {
		predeclared[name] = module
	}

	script := `
result = test_service.process(
    count=10,
    version=42
)
output_processed = result.processed
`

	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "test.star", script, predeclared)
	require.NoError(t, err)

	processedVal, ok := globals["output_processed"]
	require.True(t, ok)
	assert.Equal(t, starlark.Bool(true), processedVal)
	assert.Equal(t, int32(10), receivedCount)
	assert.Equal(t, uint32(42), receivedVersion)
}

// TestIntegration_OverflowRejectedFromStarlark tests that overflow is caught end-to-end.
func TestIntegration_OverflowRejectedFromStarlark(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("test_service.process", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return map[string]any{"ok": true}, nil
	})

	testSchema, err := Parse([]byte(`
service: test
version: "1.0"
handlers:
  test_service.process:
    description: "Process with numeric types"
    compensation_strategy: none
    params:
      count:
        type: int32
        required: true
    returns:
      ok:
        type: bool
`))
	require.NoError(t, err)

	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	thread := &starlark.Thread{Name: "test_saga"}
	setStarlarkContext(thread, starlarkCtx)

	predeclared := starlark.StringDict{
		"True":  starlark.True,
		"False": starlark.False,
		"None":  starlark.None,
	}
	for name, module := range modules {
		predeclared[name] = module
	}

	// 3 billion overflows int32 (max is ~2.1 billion)
	script := `
result = test_service.process(count=3000000000)
`

	_, err = starlark.ExecFileOptions(&syntax.FileOptions{}, thread, "test.star", script, predeclared)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "int32")
}

// TestExportedContextFunctions tests the exported context functions.
func TestExportedContextFunctions(t *testing.T) {
	starlarkCtx := &saga.StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	thread := &starlark.Thread{Name: "test"}

	// Use exported functions
	SetStarlarkContext(thread, starlarkCtx)
	retrieved := GetStarlarkContext(thread)

	require.NotNil(t, retrieved)
	assert.Equal(t, starlarkCtx.SagaExecutionID, retrieved.SagaExecutionID)
}

// TestIntegration_StarlarkSagaRunnerWithServiceModules tests the full integration
// of BuildServiceModules with StarlarkSagaRunner, verifying that service modules
// are correctly injected into the Starlark global scope during saga execution.
func TestIntegration_StarlarkSagaRunnerWithServiceModules(t *testing.T) {
	// Set up handler registry with test handlers
	registry := saga.NewHandlerRegistry()
	_ = registry.Register("position_keeping.initiate_log", func(ctx *saga.StarlarkContext, params map[string]any) (any, error) {
		return map[string]any{
			"log_id":   ctx.NewUUID(ctx.SagaExecutionID, "position_log").String(),
			"status":   "INITIATED",
			"position": params["position_id"],
		}, nil
	})
	_ = registry.Register("position_keeping.cancel_log", func(_ *saga.StarlarkContext, params map[string]any) (any, error) {
		return map[string]any{
			"log_id": params["log_id"],
			"status": "CANCELLED",
		}, nil
	})

	// Set up schema
	testSchema, err := Parse([]byte(`
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate position log"
    compensation_strategy: none
    params:
      position_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
    returns:
      log_id:
        type: string
      status:
        type: string
      position:
        type: string
  position_keeping.cancel_log:
    description: "Cancel position log"
    compensation_strategy: none
    params:
      log_id:
        type: string
        required: true
    returns:
      log_id:
        type: string
      status:
        type: string
`))
	require.NoError(t, err)

	// Build service modules
	modules, err := BuildServiceModulesFromSchema(registry, testSchema)
	require.NoError(t, err)

	// Create runtime and runner
	runtime, err := saga.NewRuntime(slog.Default(), saga.WithTimeout(10*time.Second))
	require.NoError(t, err)

	runner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       registry,
		ServiceModules: modules,
		Logger:         slog.Default(),
	})
	require.NoError(t, err)

	t.Run("typed handler calls via service modules", func(t *testing.T) {
		script := `
# Use typed service module syntax
result = position_keeping.initiate_log(
    position_id="pos-123",
    amount="100.00",
    direction="DEBIT"
)
output_log_id = result.log_id
output_status = result.status
output_position = result.position
`
		output, err := runner.ExecuteSaga(context.Background(), "typed_saga", script, saga.RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		})
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.NotEmpty(t, output.Output["output_log_id"])
		assert.Equal(t, "INITIATED", output.Output["output_status"])
		assert.Equal(t, "pos-123", output.Output["output_position"])
	})

	t.Run("service modules discoverable via dir()", func(t *testing.T) {
		script := `
# Verify service module structure
pk_attrs = dir(position_keeping)
has_initiate = "initiate_log" in pk_attrs
has_cancel = "cancel_log" in pk_attrs
`
		output, err := runner.ExecuteSaga(context.Background(), "dir_saga", script, saga.RunnerInput{
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
			Input:           map[string]interface{}{},
		})
		require.NoError(t, err)
		assert.True(t, output.Success)
		assert.Equal(t, true, output.Output["has_initiate"])
		assert.Equal(t, true, output.Output["has_cancel"])
	})
}

// TestParseHandlerTree_SinglePart tests that single-part names are ignored.
func TestParseHandlerTree_SinglePart(t *testing.T) {
	tree := parseHandlerTree([]string{
		"invalid", // Single part - should be ignored
		"valid.handler",
	})

	// Should only have "valid" as top-level
	assert.Len(t, tree.children, 1)
	assert.Contains(t, tree.children, "valid")
}

func TestAuthorizeHandlerInvocation(t *testing.T) {
	t.Run("allows handler without RBAC metadata (backward compat)", func(t *testing.T) {
		ctx := &saga.StarlarkContext{}
		def := &HandlerDef{ResourceType: "", RequiredPermission: ""}
		err := authorizeHandlerInvocation(ctx, def, "test.handler")
		assert.NoError(t, err)
	})

	t.Run("denies handler with ResourceType but no RequiredPermission (partial RBAC)", func(t *testing.T) {
		ctx := &saga.StarlarkContext{}
		def := &HandlerDef{ResourceType: "payment_order", RequiredPermission: ""}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHandlerAuthorizationDenied)
		assert.Contains(t, err.Error(), "must declare both")
	})

	t.Run("denies handler with RequiredPermission but no ResourceType (partial RBAC)", func(t *testing.T) {
		ctx := &saga.StarlarkContext{}
		def := &HandlerDef{ResourceType: "", RequiredPermission: "write"}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHandlerAuthorizationDenied)
		assert.Contains(t, err.Error(), "must declare both")
	})

	t.Run("allows system saga without Claims", func(t *testing.T) {
		ctx := &saga.StarlarkContext{Claims: nil}
		def := &HandlerDef{ResourceType: "payment_order", RequiredPermission: "write"}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create_lien")
		assert.NoError(t, err)
	})

	t.Run("allows when user has required scope", func(t *testing.T) {
		ctx := &saga.StarlarkContext{
			Claims: &auth.Claims{Scopes: []string{"payment_order:write"}},
		}
		def := &HandlerDef{ResourceType: "payment_order", RequiredPermission: "write"}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create_lien")
		assert.NoError(t, err)
	})

	t.Run("allows when user has required role", func(t *testing.T) {
		ctx := &saga.StarlarkContext{
			Claims: &auth.Claims{Roles: []string{"payment_order:write"}},
		}
		def := &HandlerDef{ResourceType: "payment_order", RequiredPermission: "write"}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create_lien")
		assert.NoError(t, err)
	})

	t.Run("denies when user lacks permission", func(t *testing.T) {
		ctx := &saga.StarlarkContext{
			Claims: &auth.Claims{
				Scopes: []string{"payment_order:read"},
				Roles:  []string{"viewer"},
			},
		}
		def := &HandlerDef{ResourceType: "payment_order", RequiredPermission: "write"}
		err := authorizeHandlerInvocation(ctx, def, "payment_order.create_lien")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHandlerAuthorizationDenied)
		assert.Contains(t, err.Error(), "requires permission")
		assert.Contains(t, err.Error(), "payment_order:write")
	})

	t.Run("denies with empty scopes and roles", func(t *testing.T) {
		ctx := &saga.StarlarkContext{
			Claims: &auth.Claims{},
		}
		def := &HandlerDef{ResourceType: "account", RequiredPermission: "execute"}
		err := authorizeHandlerInvocation(ctx, def, "account.debit")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrHandlerAuthorizationDenied)
	})
}
