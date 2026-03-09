package saga

import (
	"context"
	"errors"
	"fmt"
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

// TestHandlerRegistry_Register tests handler registration.
func TestHandlerRegistry_Register(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Verify handler is registered
	assert.True(t, registry.Has("test.handler"))
}

// TestHandlerRegistry_Register_RejectsDuplicates tests duplicate rejection.
func TestHandlerRegistry_Register_RejectsDuplicates(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Registering same name again should fail
	err = registry.Register("test.handler", handler)
	assert.ErrorIs(t, err, ErrHandlerAlreadyRegistered)
}

// TestHandlerRegistry_Register_RejectsEmptyName tests empty name rejection.
func TestHandlerRegistry_Register_RejectsEmptyName(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	err := registry.Register("", handler)
	assert.ErrorIs(t, err, ErrInvalidHandlerName)
}

// TestHandlerRegistry_Get tests handler retrieval.
func TestHandlerRegistry_Get(t *testing.T) {
	registry := NewHandlerRegistry()

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

// TestHandlerRegistry_Get_ReturnsErrorForUnknown tests unknown handler.
func TestHandlerRegistry_Get_ReturnsErrorForUnknown(t *testing.T) {
	registry := NewHandlerRegistry()

	_, err := registry.Get("unknown.handler")
	assert.ErrorIs(t, err, ErrHandlerNotFound)
}

// TestHandlerRegistry_Has tests existence checking.
func TestHandlerRegistry_Has(t *testing.T) {
	registry := NewHandlerRegistry()

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

// TestHandlerRegistry_List tests listing all handlers.
func TestHandlerRegistry_List(t *testing.T) {
	registry := NewHandlerRegistry()

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

// TestHandlerRegistry_ConcurrentAccess tests thread safety.
func TestHandlerRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewHandlerRegistry()

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

// TestStarlarkContext_ValidatePartyAccess tests party scope enforcement.
func TestStarlarkContext_ValidatePartyAccess(t *testing.T) {
	ownPartyID := uuid.New()
	allowedPartyID := uuid.New()
	forbiddenPartyID := uuid.New()

	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
		PartyScope: &PartyScope{
			PartyID:        ownPartyID,
			PartyType:      PartyTypeOrganization,
			VisibleParties: []uuid.UUID{ownPartyID, allowedPartyID},
			TenantID:       "tenant-1",
		},
	}

	t.Run("allows access to own party", func(t *testing.T) {
		err := ctx.ValidatePartyAccess(ownPartyID)
		require.NoError(t, err)
	})

	t.Run("allows access to visible party", func(t *testing.T) {
		err := ctx.ValidatePartyAccess(allowedPartyID)
		require.NoError(t, err)
	})

	t.Run("rejects access to forbidden party", func(t *testing.T) {
		err := ctx.ValidatePartyAccess(forbiddenPartyID)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrPartyScopeViolation)
		assert.Contains(t, err.Error(), forbiddenPartyID.String())
		assert.Contains(t, err.Error(), ownPartyID.String())
	})
}

func TestStarlarkContext_ValidatePartyAccess_NilScope(t *testing.T) {
	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
		PartyScope:      nil, // No party scope configured
	}

	// With nil PartyScope, all access should be allowed (backward compatibility)
	anyPartyID := uuid.New()
	err := ctx.ValidatePartyAccess(anyPartyID)
	require.NoError(t, err)
}

func TestStarlarkContext_ValidatePartyAccessFromString(t *testing.T) {
	ownPartyID := uuid.New()

	ctx := &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		Logger:          slog.Default(),
		PartyScope: &PartyScope{
			PartyID:        ownPartyID,
			PartyType:      PartyTypeIndividual,
			VisibleParties: []uuid.UUID{ownPartyID},
			TenantID:       "tenant-1",
		},
	}

	t.Run("allows valid party string", func(t *testing.T) {
		err := ctx.ValidatePartyAccessFromString(ownPartyID.String())
		require.NoError(t, err)
	})

	t.Run("rejects invalid UUID string", func(t *testing.T) {
		err := ctx.ValidatePartyAccessFromString("not-a-uuid")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
	})

	t.Run("rejects empty string", func(t *testing.T) {
		err := ctx.ValidatePartyAccessFromString("")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
	})
}

// TestRequireStringParam tests the public RequireStringParam helper.
func TestRequireStringParam(t *testing.T) {
	t.Run("returns valid string value", func(t *testing.T) {
		params := map[string]any{"name": "test-value"}
		result, err := RequireStringParam(params, "name")
		require.NoError(t, err)
		assert.Equal(t, "test-value", result)
	})

	t.Run("returns empty string as valid", func(t *testing.T) {
		params := map[string]any{"name": ""}
		result, err := RequireStringParam(params, "name")
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("handles unicode strings", func(t *testing.T) {
		params := map[string]any{"name": "测试-Тест-🎉"}
		result, err := RequireStringParam(params, "name")
		require.NoError(t, err)
		assert.Equal(t, "测试-Тест-🎉", result)
	})

	t.Run("returns ErrMissingParam when key absent", func(t *testing.T) {
		params := map[string]any{}
		_, err := RequireStringParam(params, "name")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingParam)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("returns ErrInvalidParamType for non-string", func(t *testing.T) {
		params := map[string]any{"name": 123}
		_, err := RequireStringParam(params, "name")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
		assert.Contains(t, err.Error(), "int")
	})

	t.Run("handles nil params map", func(t *testing.T) {
		_, err := RequireStringParam(nil, "name")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingParam)
	})
}

// TestRequireDecimalParam tests the public RequireDecimalParam helper.
func TestRequireDecimalParam(t *testing.T) {
	t.Run("returns valid decimal.Decimal value", func(t *testing.T) {
		expected := decimal.NewFromFloat(123.45)
		params := map[string]any{"amount": expected}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, expected.Equal(result))
	})

	t.Run("parses string to decimal", func(t *testing.T) {
		params := map[string]any{"amount": "123.45"}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(123.45).Equal(result))
	})

	t.Run("accepts float64", func(t *testing.T) {
		params := map[string]any{"amount": float64(123.45)}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(123.45).Equal(result))
	})

	t.Run("accepts int", func(t *testing.T) {
		params := map[string]any{"amount": 123}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(123).Equal(result))
	})

	t.Run("accepts int64", func(t *testing.T) {
		params := map[string]any{"amount": int64(123)}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(123).Equal(result))
	})

	t.Run("handles zero value", func(t *testing.T) {
		params := map[string]any{"amount": decimal.Zero}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.Zero.Equal(result))
	})

	t.Run("handles negative values", func(t *testing.T) {
		params := map[string]any{"amount": "-123.45"}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		assert.True(t, decimal.NewFromFloat(-123.45).Equal(result))
	})

	t.Run("handles very large values", func(t *testing.T) {
		params := map[string]any{"amount": "999999999999999999.99"}
		result, err := RequireDecimalParam(params, "amount")
		require.NoError(t, err)
		expected, _ := decimal.NewFromString("999999999999999999.99")
		assert.True(t, expected.Equal(result))
	})

	t.Run("returns ErrMissingParam when key absent", func(t *testing.T) {
		params := map[string]any{}
		_, err := RequireDecimalParam(params, "amount")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingParam)
	})

	t.Run("returns ErrInvalidParamType for invalid string", func(t *testing.T) {
		params := map[string]any{"amount": "not-a-number"}
		_, err := RequireDecimalParam(params, "amount")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
	})

	t.Run("returns ErrInvalidParamType for unsupported type", func(t *testing.T) {
		params := map[string]any{"amount": true}
		_, err := RequireDecimalParam(params, "amount")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
		assert.Contains(t, err.Error(), "bool")
	})
}

// TestRequireUUIDParam tests the public RequireUUIDParam helper.
func TestRequireUUIDParam(t *testing.T) {
	validUUID := uuid.MustParse("12345678-1234-1234-1234-123456789012")

	t.Run("returns valid uuid.UUID value", func(t *testing.T) {
		params := map[string]any{"id": validUUID}
		result, err := RequireUUIDParam(params, "id")
		require.NoError(t, err)
		assert.Equal(t, validUUID, result)
	})

	t.Run("parses valid UUID string", func(t *testing.T) {
		params := map[string]any{"id": "12345678-1234-1234-1234-123456789012"}
		result, err := RequireUUIDParam(params, "id")
		require.NoError(t, err)
		assert.Equal(t, validUUID, result)
	})

	t.Run("parses UUID without hyphens", func(t *testing.T) {
		params := map[string]any{"id": "12345678123412341234123456789012"}
		result, err := RequireUUIDParam(params, "id")
		require.NoError(t, err)
		assert.Equal(t, validUUID, result)
	})

	t.Run("returns ErrMissingParam when key absent", func(t *testing.T) {
		params := map[string]any{}
		_, err := RequireUUIDParam(params, "id")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingParam)
	})

	t.Run("returns ErrInvalidParamType for malformed UUID string", func(t *testing.T) {
		params := map[string]any{"id": "not-a-uuid"}
		_, err := RequireUUIDParam(params, "id")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
		assert.Contains(t, err.Error(), "not-a-uuid")
	})

	t.Run("returns ErrInvalidParamType for wrong type", func(t *testing.T) {
		params := map[string]any{"id": 12345}
		_, err := RequireUUIDParam(params, "id")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
		assert.Contains(t, err.Error(), "int")
	})

	t.Run("handles nil UUID", func(t *testing.T) {
		params := map[string]any{"id": uuid.Nil}
		result, err := RequireUUIDParam(params, "id")
		require.NoError(t, err)
		assert.Equal(t, uuid.Nil, result)
	})
}

// TestRequireDirectionParam tests the public RequireDirectionParam helper.
func TestRequireDirectionParam(t *testing.T) {
	t.Run("accepts DEBIT", func(t *testing.T) {
		params := map[string]any{"direction": "DEBIT"}
		result, err := RequireDirectionParam(params, "direction")
		require.NoError(t, err)
		assert.Equal(t, "DEBIT", result)
	})

	t.Run("accepts CREDIT", func(t *testing.T) {
		params := map[string]any{"direction": "CREDIT"}
		result, err := RequireDirectionParam(params, "direction")
		require.NoError(t, err)
		assert.Equal(t, "CREDIT", result)
	})

	t.Run("returns ErrMissingParam when key absent", func(t *testing.T) {
		params := map[string]any{}
		_, err := RequireDirectionParam(params, "direction")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingParam)
	})

	t.Run("returns ErrInvalidDirection for lowercase", func(t *testing.T) {
		params := map[string]any{"direction": "debit"}
		_, err := RequireDirectionParam(params, "direction")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDirection)
	})

	t.Run("returns ErrInvalidDirection for invalid value", func(t *testing.T) {
		params := map[string]any{"direction": "INVALID"}
		_, err := RequireDirectionParam(params, "direction")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidDirection)
		assert.Contains(t, err.Error(), "INVALID")
	})

	t.Run("returns ErrInvalidParamType for non-string", func(t *testing.T) {
		params := map[string]any{"direction": 123}
		_, err := RequireDirectionParam(params, "direction")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParamType)
	})
}

// TestHandlerRegistry_RegisterWithMetadata tests handler registration with metadata.
func TestHandlerRegistry_RegisterWithMetadata(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	metadata := &HandlerMetadata{
		Category:            HandlerCategorySettlement,
		ProducesInstruments: []string{"USD"},
	}

	err := registry.RegisterWithMetadata("test.handler", handler, metadata)
	require.NoError(t, err)

	// Verify handler is registered
	assert.True(t, registry.Has("test.handler"))

	// Verify metadata is stored
	h, md, err := registry.GetWithMetadata("test.handler")
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NotNil(t, md)
	assert.Equal(t, HandlerCategorySettlement, md.Category)
	assert.Equal(t, []string{"USD"}, md.ProducesInstruments)
}

// TestHandlerRegistry_GetWithMetadata tests handler retrieval with metadata.
func TestHandlerRegistry_GetWithMetadata(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	metadata := &HandlerMetadata{
		Category:            HandlerCategoryIngestion,
		ProducesInstruments: []string{"KWH", "GAS"},
	}

	err := registry.RegisterWithMetadata("test.handler", handler, metadata)
	require.NoError(t, err)

	// Get with metadata
	h, md, err := registry.GetWithMetadata("test.handler")
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NotNil(t, md)
	assert.Equal(t, HandlerCategoryIngestion, md.Category)
	assert.Equal(t, []string{"KWH", "GAS"}, md.ProducesInstruments)
}

// TestHandlerRegistry_GetWithMetadata_NoMetadata tests backward compatibility.
func TestHandlerRegistry_GetWithMetadata_NoMetadata(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	// Register without metadata (backward compatibility)
	err := registry.Register("test.handler", handler)
	require.NoError(t, err)

	// Get with metadata should return nil metadata
	h, md, err := registry.GetWithMetadata("test.handler")
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Nil(t, md, "handlers registered without metadata should return nil metadata")
}

// TestHandlerRegistry_AllWithMetadata tests retrieving all handlers with metadata.
func TestHandlerRegistry_AllWithMetadata(t *testing.T) {
	t.Run("returns empty map for empty registry", func(t *testing.T) {
		registry := NewHandlerRegistry()
		result := registry.AllWithMetadata()
		assert.Empty(t, result)
	})

	t.Run("returns all registered handlers with metadata", func(t *testing.T) {
		registry := NewHandlerRegistry()

		handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
			return nil, nil
		}

		meta1 := &HandlerMetadata{
			Category:    HandlerCategoryIngestion,
			Description: "Ingest meter readings",
		}
		meta2 := &HandlerMetadata{
			Category:    HandlerCategorySettlement,
			Description: "Settle positions",
		}

		require.NoError(t, registry.RegisterWithMetadata("handler.a", handler, meta1))
		require.NoError(t, registry.RegisterWithMetadata("handler.b", handler, meta2))

		result := registry.AllWithMetadata()
		assert.Len(t, result, 2)
		assert.Equal(t, meta1, result["handler.a"])
		assert.Equal(t, meta2, result["handler.b"])
	})

	t.Run("returns nil metadata for handlers registered without metadata", func(t *testing.T) {
		registry := NewHandlerRegistry()

		handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
			return nil, nil
		}

		require.NoError(t, registry.Register("handler.no-meta", handler))

		result := registry.AllWithMetadata()
		assert.Len(t, result, 1)
		assert.Nil(t, result["handler.no-meta"])
	})

	t.Run("returns a copy that does not affect registry", func(t *testing.T) {
		registry := NewHandlerRegistry()

		handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
			return nil, nil
		}

		meta := &HandlerMetadata{Category: HandlerCategoryValuation}
		require.NoError(t, registry.RegisterWithMetadata("handler.x", handler, meta))

		result := registry.AllWithMetadata()
		// Mutate the returned map
		delete(result, "handler.x")

		// Registry should be unaffected
		assert.True(t, registry.Has("handler.x"))
		assert.Len(t, registry.AllWithMetadata(), 1)
	})
}

// TestHandlerCategory_Values tests handler category constants.
func TestHandlerCategory_Values(t *testing.T) {
	assert.Equal(t, HandlerCategory("ingestion"), HandlerCategoryIngestion)
	assert.Equal(t, HandlerCategory("settlement"), HandlerCategorySettlement)
	assert.Equal(t, HandlerCategory("valuation"), HandlerCategoryValuation)
}

func TestStarlarkContext_IdempotencyKey(t *testing.T) {
	t.Run("struct has IdempotencyKey field", func(t *testing.T) {
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			IdempotencyKey:  "saga_abc123_step_5",
			Logger:          slog.Default(),
		}

		assert.Equal(t, "saga_abc123_step_5", ctx.IdempotencyKey, "should store idempotency key")
	})

	t.Run("can be empty", func(t *testing.T) {
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			Logger:          slog.Default(),
		}

		assert.Empty(t, ctx.IdempotencyKey, "idempotency key should default to empty")
	})

	t.Run("follows saga key format", func(t *testing.T) {
		executionID := uuid.New()
		expectedKey := "saga_" + executionID.String() + "_step_3"

		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: executionID,
			IdempotencyKey:  expectedKey,
			Logger:          slog.Default(),
		}

		assert.Contains(t, ctx.IdempotencyKey, "saga_", "should contain saga prefix")
		assert.Contains(t, ctx.IdempotencyKey, executionID.String(), "should contain execution ID")
		assert.Contains(t, ctx.IdempotencyKey, "step_", "should contain step marker")
		assert.Contains(t, ctx.IdempotencyKey, "3", "should contain step index")
	})
}

func TestStarlarkContext_NextIdempotencyKey(t *testing.T) {
	t.Run("generates sequential keys", func(t *testing.T) {
		executionID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: executionID,
			Logger:          slog.Default(),
		}

		key1 := ctx.NextIdempotencyKey()
		key2 := ctx.NextIdempotencyKey()
		key3 := ctx.NextIdempotencyKey()

		assert.Equal(t, "saga_11111111-1111-1111-1111-111111111111_step_1", key1)
		assert.Equal(t, "saga_11111111-1111-1111-1111-111111111111_step_2", key2)
		assert.Equal(t, "saga_11111111-1111-1111-1111-111111111111_step_3", key3)
	})

	t.Run("is thread-safe", func(t *testing.T) {
		executionID := uuid.New()
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: executionID,
			Logger:          slog.Default(),
		}

		// Generate keys concurrently
		const goroutines = 10
		const keysPerGoroutine = 10
		keys := make([]string, goroutines*keysPerGoroutine)
		var wg sync.WaitGroup

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(offset int) {
				defer wg.Done()
				for j := 0; j < keysPerGoroutine; j++ {
					keys[offset*keysPerGoroutine+j] = ctx.NextIdempotencyKey()
				}
			}(i)
		}

		wg.Wait()

		// All keys should be unique
		keySet := make(map[string]bool)
		for _, key := range keys {
			require.False(t, keySet[key], "duplicate key generated: %s", key)
			keySet[key] = true
		}

		// Should have exactly the expected number of unique keys
		assert.Len(t, keySet, goroutines*keysPerGoroutine, "should generate unique keys")

		// Final counter should equal the number of keys generated
		finalKey := ctx.NextIdempotencyKey()
		expectedStep := goroutines*keysPerGoroutine + 1
		assert.Contains(t, finalKey, fmt.Sprintf("_step_%d", expectedStep), "counter should be accurate")
	})

	t.Run("deterministic for same execution ID", func(t *testing.T) {
		executionID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

		// Create two contexts with same execution ID
		ctx1 := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: executionID,
			Logger:          slog.Default(),
		}

		ctx2 := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: executionID,
			Logger:          slog.Default(),
		}

		// Generate keys in same order
		key1_1 := ctx1.NextIdempotencyKey()
		key1_2 := ctx1.NextIdempotencyKey()

		key2_1 := ctx2.NextIdempotencyKey()
		key2_2 := ctx2.NextIdempotencyKey()

		// Keys should match for same step number in replay scenario
		assert.Equal(t, key1_1, key2_1, "first step should generate same key")
		assert.Equal(t, key1_2, key2_2, "second step should generate same key")
	})
}
