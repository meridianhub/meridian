package saga

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStarlarkContext_NewUUID tests deterministic UUID generation.
func TestStarlarkContext_NewUUID(t *testing.T) {
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Logger:          slog.Default(),
	}

	// Same namespace + name should produce same UUID (deterministic)
	namespace := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	uuid1 := ctx.NewUUID(namespace, "test-step")
	uuid2 := ctx.NewUUID(namespace, "test-step")
	assert.Equal(t, uuid1, uuid2, "same namespace + name should produce same UUID")

	// Different name should produce different UUID
	uuid3 := ctx.NewUUID(namespace, "different-step")
	assert.NotEqual(t, uuid1, uuid3, "different names should produce different UUIDs")
}

// TestStarlarkContext_EmitProgress tests progress emission.
func TestStarlarkContext_EmitProgress(t *testing.T) {
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	// Valid percentage should succeed
	err := ctx.EmitProgress("step1", 50, "halfway done")
	require.NoError(t, err)

	// Invalid percentage (negative) should fail
	err = ctx.EmitProgress("step1", -1, "invalid")
	assert.Error(t, err)

	// Invalid percentage (>100) should fail
	err = ctx.EmitProgress("step1", 101, "invalid")
	assert.Error(t, err)
}

// TestStarlarkContext_Suspend tests suspension creation.
func TestStarlarkContext_Suspend(t *testing.T) {
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	err := ctx.Suspend("waiting for approval", 1*time.Hour)
	require.NoError(t, err)
	assert.True(t, ctx.IsSuspended())
	assert.Equal(t, "waiting for approval", ctx.SuspendReason)
}

// TestStarlarkContext_ContextCancellation tests context propagation.
func TestStarlarkContext_ContextCancellation(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())

	ctx := &StarlarkContext{
		Context:         parentCtx,
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	// Not cancelled yet
	assert.NoError(t, ctx.Err())

	cancel()

	// Now cancelled
	assert.Error(t, ctx.Err())
	assert.True(t, errors.Is(ctx.Err(), context.Canceled))
}

// TestStepHandlerRegistry_Register tests handler registration.
func TestStepHandlerRegistry_Register(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Verify handler is registered
	assert.True(t, registry.Has("test.handler"))
}

// TestStepHandlerRegistry_Register_RejectsDuplicates tests duplicate rejection.
func TestStepHandlerRegistry_Register_RejectsDuplicates(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Registering same name again should fail
	err = registry.Register("test.handler", handler)
	assert.ErrorIs(t, err, ErrHandlerAlreadyRegistered)
}

// TestStepHandlerRegistry_Register_RejectsEmptyName tests empty name rejection.
func TestStepHandlerRegistry_Register_RejectsEmptyName(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("", handler)
	assert.ErrorIs(t, err, ErrInvalidHandlerName)
}

// TestStepHandlerRegistry_Get tests handler retrieval.
func TestStepHandlerRegistry_Get(t *testing.T) {
	registry := NewStepHandlerRegistry()

	expectedResult := "test-result"
	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return expectedResult, nil
	}

	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Get the handler
	h, err := registry.Get("test.handler")
	require.NoError(t, err)
	require.NotNil(t, h)

	// Execute it and verify result
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}
	result, err := h(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

// TestStepHandlerRegistry_Get_ReturnsErrorForUnknown tests unknown handler.
func TestStepHandlerRegistry_Get_ReturnsErrorForUnknown(t *testing.T) {
	registry := NewStepHandlerRegistry()

	_, err := registry.Get("unknown.handler")
	assert.ErrorIs(t, err, ErrHandlerNotFound)
}

// TestStepHandlerRegistry_Has tests existence checking.
func TestStepHandlerRegistry_Has(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	// Before registration
	assert.False(t, registry.Has("test.handler"))

	// After registration
	err := registry.Register("test.handler", handler)
	require.NoError(t, err)
	assert.True(t, registry.Has("test.handler"))
}

// TestStepHandlerRegistry_List tests listing all handlers.
func TestStepHandlerRegistry_List(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	// Register multiple handlers
	require.NoError(t, registry.Register("z.handler", handler))
	require.NoError(t, registry.Register("a.handler", handler))
	require.NoError(t, registry.Register("m.handler", handler))

	// List should be sorted
	list := registry.List()
	assert.Equal(t, []string{"a.handler", "m.handler", "z.handler"}, list)
}

// TestStepHandlerRegistry_ConcurrentAccess tests thread safety.
func TestStepHandlerRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewStepHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}

	var wg sync.WaitGroup
	const numGoroutines = 100

	// Concurrent registrations (with unique names)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := "handler." + string(rune('a'+id%26)) + string(rune('0'+id/26))
			_ = registry.Register(name, handler)
		}(i)
	}

	// Concurrent reads while registrations are happening
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = registry.List()
			_ = registry.Has("handler.a0")
		}()
	}

	wg.Wait()

	// Verify registry is consistent
	list := registry.List()
	assert.Greater(t, len(list), 0)
}

// TestDefaultHandlers_Registered tests that default handlers are registered.
func TestDefaultHandlers_Registered(t *testing.T) {
	registry := DefaultRegistry()

	expectedHandlers := []string{
		"position_keeping.initiate_log",
		"position_keeping.update_log",
		"position_keeping.cancel_log",
		"financial_accounting.post_entries",
		"financial_accounting.reverse_entries",
		"financial_accounting.create_booking",
		"current_account.create_lien",
		"current_account.execute_lien",
		"current_account.terminate_lien",
		"valuation_engine.valuate",
		"repository.save",
		"notification.send",
	}

	for _, name := range expectedHandlers {
		assert.True(t, registry.Has(name), "expected handler %q to be registered", name)
	}
}

// TestHandler_PositionKeeping_InitiateLog tests position keeping initiate log handler.
func TestHandler_PositionKeeping_InitiateLog(t *testing.T) {
	registry := DefaultRegistry()
	handler, err := registry.Get("position_keeping.initiate_log")
	require.NoError(t, err)

	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	t.Run("valid params", func(t *testing.T) {
		params := map[string]any{
			"position_id": uuid.New().String(),
			"amount":      decimal.NewFromInt(100),
			"direction":   "DEBIT",
		}
		result, err := handler(ctx, params)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("missing position_id", func(t *testing.T) {
		params := map[string]any{
			"amount":    decimal.NewFromInt(100),
			"direction": "DEBIT",
		}
		_, err := handler(ctx, params)
		assert.Error(t, err)
	})
}

// TestHandler_ValuationEngine_Valuate tests valuation handler returns Decimal.
func TestHandler_ValuationEngine_Valuate(t *testing.T) {
	registry := DefaultRegistry()
	handler, err := registry.Get("valuation_engine.valuate")
	require.NoError(t, err)

	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		KnowledgeAt:     time.Now(),
		Logger:          slog.Default(),
	}

	params := map[string]any{
		"instrument":   "ELEC-SPOT-NZ",
		"quantity":     decimal.NewFromInt(1000),
		"context_type": "MARKET",
	}

	result, err := handler(ctx, params)
	require.NoError(t, err)

	// Result should contain a Decimal value
	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "result should be a map")

	value, ok := resultMap["value"]
	require.True(t, ok, "result should contain 'value' key")

	_, ok = value.(decimal.Decimal)
	assert.True(t, ok, "value should be a decimal.Decimal")
}

// TestHandler_ErrorWrapping tests that handler errors include context.
func TestHandler_ErrorWrapping(t *testing.T) {
	registry := DefaultRegistry()
	handler, err := registry.Get("financial_accounting.post_entries")
	require.NoError(t, err)

	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
	}

	// Invalid params should return wrapped error
	params := map[string]any{
		"invalid": "params",
	}
	_, err = handler(ctx, params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "financial_accounting.post_entries")
}
