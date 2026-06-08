package saga

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// newStarlarkCtx builds a minimal StarlarkContext for thread-local setup.
func newStarlarkCtx() *StarlarkContext {
	return &StarlarkContext{
		Context:         context.Background(),
		SagaExecutionID: uuid.New(),
		CorrelationID:   uuid.New(),
		KnowledgeAt:     time.Now(),
		LookupCache:     NewLookupResultCache(),
	}
}

// TestGetThreadLocal covers the generic thread-local extractor across its three
// outcomes: missing key, wrong type, and successful typed retrieval.
func TestGetThreadLocal(t *testing.T) {
	t.Run("missing returns ErrMissingClient", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		_, err := getThreadLocal[*Composer](thread, "saga.Composer", "invoke_saga")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
		assert.Contains(t, err.Error(), "invoke_saga")
	})

	t.Run("wrong type returns ErrInvalidClientType", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.Composer", "not-a-composer")
		_, err := getThreadLocal[*Composer](thread, "saga.Composer", "invoke_saga")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidClientType)
	})

	t.Run("correct type returns value", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		stack := NewCallStack()
		thread.SetLocal("saga.CallStack", stack)
		got, err := getThreadLocal[*CallStack](thread, "saga.CallStack", "invoke_saga")
		require.NoError(t, err)
		assert.Same(t, stack, got)
	})
}

// TestGetStarlarkContext_InvalidType covers the wrong-type branch of
// getStarlarkContext (the missing branch is covered in builtins_dsl_test.go).
func TestGetStarlarkContext_InvalidType(t *testing.T) {
	thread := &starlark.Thread{Name: "test"}
	thread.SetLocal("saga.StarlarkContext", "not-a-context")
	_, err := getStarlarkContext(thread, "cel_eval")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStarlarkContext)
	assert.Contains(t, err.Error(), "cel_eval")
}

// TestGetRefDataClient covers both missing and wrong-type branches.
func TestGetRefDataClient(t *testing.T) {
	t.Run("missing returns ErrMissingClient", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		_, err := getRefDataClient(thread, "resolve_account")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
	})

	t.Run("wrong type returns ErrInvalidClientType", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.ReferenceDataClient", 12345)
		_, err := getRefDataClient(thread, "resolve_account")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidClientType)
	})

	t.Run("correct type returns client", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		client := newMockReferenceDataClient()
		thread.SetLocal("saga.ReferenceDataClient", client)
		got, err := getRefDataClient(thread, "resolve_account")
		require.NoError(t, err)
		assert.Same(t, client, got)
	})
}

// TestResolveInstrumentBuiltin_ErrorPaths covers the missing-context,
// missing-client, and unknown-instrument branches not exercised by the
// happy-path tests in builtins_dsl_test.go.
func TestResolveInstrumentBuiltin_ErrorPaths(t *testing.T) {
	builtins := NewRestrictedBuiltins(nil)
	resolveInstrument := builtins["resolve_instrument"].(*starlark.Builtin)

	t.Run("missing context", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		_, err := resolveInstrument.CallInternal(thread, starlark.Tuple{starlark.String("USD")}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingStarlarkContext)
	})

	t.Run("missing client", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		_, err := resolveInstrument.CallInternal(thread, starlark.Tuple{starlark.String("USD")}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
	})

	t.Run("unknown instrument propagates error", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		thread.SetLocal("saga.ReferenceDataClient", newMockReferenceDataClient())
		_, err := resolveInstrument.CallInternal(thread, starlark.Tuple{starlark.String("UNKNOWN")}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instrument not found")
	})
}

// TestResolveAccountBuiltin_MissingContext covers the missing-context branch of
// resolve_account, complementing the missing-client/unknown cases already tested.
func TestResolveAccountBuiltin_MissingContext(t *testing.T) {
	builtins := NewRestrictedBuiltins(nil)
	resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

	thread := &starlark.Thread{Name: "test"}
	_, err := resolveAccount.CallInternal(thread, starlark.Tuple{starlark.String("customer-001")}, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingStarlarkContext)
}

// TestCachedLookup_NilCache covers the nil-cache early return of cachedLookup.
func TestCachedLookup_NilCache(t *testing.T) {
	ctx := &StarlarkContext{} // LookupCache is nil
	val, ok := cachedLookup(ctx, "account:foo")
	assert.False(t, ok)
	assert.Empty(t, val)

	// cacheLookup with nil cache must be a no-op (no panic).
	cacheLookup(ctx, "account:foo", "bar")
}

// invokeSagaBuiltin returns the invoke_saga builtin registered in the
// restricted environment (the addCompositionBuiltins variant, distinct from
// Composer.InvokeSagaBuiltin).
func invokeSagaBuiltin(t *testing.T) *starlark.Builtin {
	t.Helper()
	builtins := NewRestrictedBuiltins(nil)
	fn, ok := builtins["invoke_saga"].(*starlark.Builtin)
	require.True(t, ok)
	return fn
}

// TestInvokeSagaBuiltin_RestrictedEnv drives the addCompositionBuiltins
// invoke_saga closure through its thread-local checks, dict conversion, and a
// successful child invocation (also covering starlarkDictToGoMap).
func TestInvokeSagaBuiltin_RestrictedEnv(t *testing.T) {
	t.Run("missing StarlarkContext", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		_, err := invokeSagaBuiltin(t).CallInternal(thread, starlark.Tuple{starlark.String("child")}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingStarlarkContext)
	})

	t.Run("missing Composer", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		_, err := invokeSagaBuiltin(t).CallInternal(thread, starlark.Tuple{starlark.String("child")}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
	})

	t.Run("missing CallStack", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		thread.SetLocal("saga.Composer", NewComposer(nil, nil))
		_, err := invokeSagaBuiltin(t).CallInternal(thread, starlark.Tuple{starlark.String("child")}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
	})

	t.Run("non-string input key rejected", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		thread.SetLocal("saga.Composer", NewComposer(nil, nil))
		thread.SetLocal("saga.CallStack", NewCallStack())

		badInput := starlark.NewDict(1)
		_ = badInput.SetKey(starlark.MakeInt(1), starlark.String("v"))
		_, err := invokeSagaBuiltin(t).CallInternal(thread, starlark.Tuple{starlark.String("child"), badInput}, nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParameterType)
		assert.Contains(t, err.Error(), "input keys must be strings")
	})

	t.Run("successful invocation returns sagaResultValue", func(t *testing.T) {
		mockRegistry := &MockRegistry{
			Sagas: map[string]*MockSagaDef{
				"child": {Name: "child", Version: 1, Script: "x = 1\n", StepsCompleted: 2},
			},
		}
		composer := NewComposer(mockRegistry, nil)

		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		thread.SetLocal("saga.Composer", composer)
		thread.SetLocal("saga.CallStack", NewCallStack())

		input := starlark.NewDict(1)
		_ = input.SetKey(starlark.String("amount"), starlark.MakeInt(100))

		result, err := invokeSagaBuiltin(t).CallInternal(thread,
			starlark.Tuple{starlark.String("child"), input}, nil)
		require.NoError(t, err)

		srv, ok := result.(*sagaResultValue)
		require.True(t, ok)
		assert.Equal(t, ResultStatusCompleted, srv.status)
		assert.Equal(t, 2, srv.stepsCompleted)
	})

	t.Run("invocation error is wrapped", func(t *testing.T) {
		// Empty registry → child not found → InvokeSaga errors.
		composer := NewComposer(&MockRegistry{Sagas: map[string]*MockSagaDef{}}, nil)

		thread := &starlark.Thread{Name: "test"}
		thread.SetLocal("saga.StarlarkContext", newStarlarkCtx())
		thread.SetLocal("saga.Composer", composer)
		thread.SetLocal("saga.CallStack", NewCallStack())

		_, err := invokeSagaBuiltin(t).CallInternal(thread,
			starlark.Tuple{starlark.String("nope")}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invoke_saga(\"nope\")")
	})
}

// TestStarlarkDictToGoMap covers the converter directly, including the
// non-string-key error branch and a nested-value conversion.
func TestStarlarkDictToGoMap(t *testing.T) {
	t.Run("converts string-keyed dict", func(t *testing.T) {
		d := starlark.NewDict(2)
		_ = d.SetKey(starlark.String("a"), starlark.MakeInt(1))
		nested := starlark.NewList([]starlark.Value{starlark.String("x")})
		_ = d.SetKey(starlark.String("b"), nested)

		got, err := starlarkDictToGoMap(d, "invoke_saga")
		require.NoError(t, err)
		assert.Equal(t, 1, got["a"])
		assert.Equal(t, []interface{}{"x"}, got["b"])
	})

	t.Run("rejects non-string key", func(t *testing.T) {
		d := starlark.NewDict(1)
		_ = d.SetKey(starlark.MakeInt(7), starlark.String("v"))
		_, err := starlarkDictToGoMap(d, "invoke_saga")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParameterType)
	})
}

// TestConvertStarlarkToGo_EdgeCases covers the nil-input guard and the default
// branch (an unrecognized Starlark value falls back to String()).
func TestConvertStarlarkToGo_EdgeCases(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		assert.Nil(t, convertStarlarkToGo(nil))
	})

	t.Run("default branch falls back to String", func(t *testing.T) {
		// A *sagaDefinitionValue is not a recognized conversion type, so the
		// default arm returns its String() representation.
		def := &sagaDefinitionValue{name: "demo"}
		got := convertStarlarkToGo(def)
		assert.Equal(t, `saga("demo")`, got)
	})
}
