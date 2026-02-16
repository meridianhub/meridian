package saga

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// mockReferenceDataClient implements ReferenceDataClient for testing.
type mockReferenceDataClient struct {
	accounts    map[string]string
	instruments map[string]string
	calls       []string // Track method calls for verification
}

func newMockReferenceDataClient() *mockReferenceDataClient {
	return &mockReferenceDataClient{
		accounts: map[string]string{
			"customer-001": "account-123",
			"customer-002": "account-456",
			"party:party-123:org:org-456:currency:GBP": "org-account-789",
		},
		instruments: map[string]string{
			"USD": "instr-usd",
			"KWH": "instr-kwh",
		},
		calls: []string{},
	}
}

func (m *mockReferenceDataClient) ResolveAccount(_ context.Context, reference string, _ time.Time) (string, error) {
	m.calls = append(m.calls, fmt.Sprintf("ResolveAccount(%s)", reference))
	if accountID, ok := m.accounts[reference]; ok {
		return accountID, nil
	}
	return "", fmt.Errorf("account not found: %s", reference)
}

func (m *mockReferenceDataClient) ResolveInstrument(_ context.Context, reference string, _ time.Time) (string, error) {
	m.calls = append(m.calls, fmt.Sprintf("ResolveInstrument(%s)", reference))
	if instrumentID, ok := m.instruments[reference]; ok {
		return instrumentID, nil
	}
	return "", fmt.Errorf("instrument not found: %s", reference)
}

func TestCelEvalBuiltin(t *testing.T) {
	t.Run("evaluates simple expression", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)

		builtins := NewRestrictedBuiltins(nil)
		celEval := builtins["cel_eval"].(*starlark.Builtin)

		// Call cel_eval("1 + 1")
		args := starlark.Tuple{starlark.String("1 + 1")}
		result, err := celEval.CallInternal(thread, args, nil)

		require.NoError(t, err)
		assert.Equal(t, "2", result.String())
	})

	t.Run("evaluates expression with input variables", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)

		builtins := NewRestrictedBuiltins(nil)
		celEval := builtins["cel_eval"].(*starlark.Builtin)

		// Call cel_eval("input.amount > 100", {"amount": 150})
		inputDict := starlark.NewDict(1)
		_ = inputDict.SetKey(starlark.String("amount"), starlark.MakeInt(150))
		args := starlark.Tuple{starlark.String("input.amount > 100"), inputDict}
		result, err := celEval.CallInternal(thread, args, nil)

		require.NoError(t, err)
		assert.Equal(t, "True", result.String())
	})

	t.Run("rejects non-string keys in variables", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:         context.Background(),
			SagaExecutionID: uuid.New(),
			CorrelationID:   uuid.New(),
			KnowledgeAt:     time.Now(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)

		builtins := NewRestrictedBuiltins(nil)
		celEval := builtins["cel_eval"].(*starlark.Builtin)

		// Call cel_eval with non-string key
		inputDict := starlark.NewDict(1)
		_ = inputDict.SetKey(starlark.MakeInt(42), starlark.String("value"))
		args := starlark.Tuple{starlark.String("1 + 1"), inputDict}
		_, err := celEval.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidParameterType)
		assert.Contains(t, err.Error(), "variables keys must be strings")
	})

	t.Run("handles missing context", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		// Don't set StarlarkContext

		builtins := NewRestrictedBuiltins(nil)
		celEval := builtins["cel_eval"].(*starlark.Builtin)

		args := starlark.Tuple{starlark.String("1 + 1")}
		_, err := celEval.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingStarlarkContext)
	})
}

func TestResolveAccountBuiltin(t *testing.T) {
	t.Run("resolves account successfully", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		// Call resolve_account("customer-001")
		args := starlark.Tuple{starlark.String("customer-001")}
		result, err := resolveAccount.CallInternal(thread, args, nil)

		require.NoError(t, err)
		assert.Equal(t, `"account-123"`, result.String())
		assert.Contains(t, mockClient.calls, "ResolveAccount(customer-001)")
	})

	t.Run("uses cache on second call", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		// First call
		args := starlark.Tuple{starlark.String("customer-001")}
		result1, err := resolveAccount.CallInternal(thread, args, nil)
		require.NoError(t, err)
		assert.Equal(t, `"account-123"`, result1.String())
		assert.Len(t, mockClient.calls, 1)

		// Second call - should use cache
		result2, err := resolveAccount.CallInternal(thread, args, nil)
		require.NoError(t, err)
		assert.Equal(t, `"account-123"`, result2.String())
		assert.Len(t, mockClient.calls, 1, "Should not make second client call (cached)")
	})

	t.Run("handles unknown account", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		args := starlark.Tuple{starlark.String("unknown")}
		_, err := resolveAccount.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "account not found")
	})

	t.Run("handles missing client", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		// Don't set ReferenceDataClient

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		args := starlark.Tuple{starlark.String("customer-001")}
		_, err := resolveAccount.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingClient)
	})
}

func TestResolveInstrumentBuiltin(t *testing.T) {
	t.Run("resolves instrument successfully", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveInstrument := builtins["resolve_instrument"].(*starlark.Builtin)

		// Call resolve_instrument("USD")
		args := starlark.Tuple{starlark.String("USD")}
		result, err := resolveInstrument.CallInternal(thread, args, nil)

		require.NoError(t, err)
		assert.Equal(t, `"instr-usd"`, result.String())
		assert.Contains(t, mockClient.calls, "ResolveInstrument(USD)")
	})

	t.Run("uses cache on second call", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveInstrument := builtins["resolve_instrument"].(*starlark.Builtin)

		// First call
		args := starlark.Tuple{starlark.String("KWH")}
		result1, err := resolveInstrument.CallInternal(thread, args, nil)
		require.NoError(t, err)
		assert.Equal(t, `"instr-kwh"`, result1.String())
		assert.Len(t, mockClient.calls, 1)

		// Second call - should use cache
		result2, err := resolveInstrument.CallInternal(thread, args, nil)
		require.NoError(t, err)
		assert.Equal(t, `"instr-kwh"`, result2.String())
		assert.Len(t, mockClient.calls, 1, "Should not make second client call (cached)")
	})
}

func TestConvertStarlarkToGo(t *testing.T) {
	tests := []struct {
		name  string
		input starlark.Value
		want  interface{}
	}{
		{
			name:  "string",
			input: starlark.String("test"),
			want:  "test",
		},
		{
			name:  "int",
			input: starlark.MakeInt(42),
			want:  int(42), // Now returns int for values that fit
		},
		{
			name:  "float",
			input: starlark.Float(3.14),
			want:  float64(3.14),
		},
		{
			name:  "bool true",
			input: starlark.Bool(true),
			want:  true,
		},
		{
			name:  "bool false",
			input: starlark.Bool(false),
			want:  false,
		},
		{
			name:  "None",
			input: starlark.None,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStarlarkToGo(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}

	// Test round-trip preservation: Go int -> Starlark -> Go should preserve type
	t.Run("round-trip preserves int type", func(t *testing.T) {
		originalInt := int(123)
		starlarkVal := goToStarlark(originalInt)
		result := convertStarlarkToGo(starlarkVal)
		assert.Equal(t, originalInt, result)
		assert.IsType(t, int(0), result, "should preserve int type on round-trip")
	})

	t.Run("int64 values convert to int when they fit", func(t *testing.T) {
		// On 64-bit systems, int and int64 have same range, so int64 values
		// are converted to int when they fit in int (which is always on amd64)
		originalInt64 := int64(9223372036854775807)
		starlarkVal := goToStarlark(originalInt64)
		result := convertStarlarkToGo(starlarkVal)
		// On 64-bit systems, result will be int (same value, different type)
		// On 32-bit systems, this would overflow int and remain int64
		// We verify the numeric value is preserved
		switch v := result.(type) {
		case int:
			assert.Equal(t, int64(v), originalInt64, "numeric value should be preserved")
		case int64:
			assert.Equal(t, v, originalInt64, "numeric value should be preserved")
		default:
			t.Fatalf("unexpected type: %T", result)
		}
	})

	t.Run("list", func(t *testing.T) {
		input := starlark.NewList([]starlark.Value{
			starlark.String("a"),
			starlark.String("b"),
		})
		result := convertStarlarkToGo(input)
		expected := []interface{}{"a", "b"}
		assert.Equal(t, expected, result)
	})

	t.Run("dict", func(t *testing.T) {
		input := starlark.NewDict(1)
		_ = input.SetKey(starlark.String("key"), starlark.String("value"))
		result := convertStarlarkToGo(input)
		expected := map[string]interface{}{"key": "value"}
		assert.Equal(t, expected, result)
	})

	t.Run("nested dict with non-string keys logs warning", func(t *testing.T) {
		// Create nested dict with non-string key
		nestedDict := starlark.NewDict(2)
		_ = nestedDict.SetKey(starlark.String("valid"), starlark.String("value"))
		_ = nestedDict.SetKey(starlark.MakeInt(42), starlark.String("invalid"))

		result := convertStarlarkToGo(nestedDict)

		// Should only contain the valid key, non-string key is logged and dropped
		expected := map[string]interface{}{"valid": "value"}
		assert.Equal(t, expected, result)
	})
}

func TestBuildOrgAccountRefBuiltin(t *testing.T) {
	t.Run("builds valid composite reference", func(t *testing.T) {
		builtins := NewRestrictedBuiltins(nil)
		buildRef := builtins["build_org_account_ref"].(*starlark.Builtin)

		thread := &starlark.Thread{Name: "test"}
		result, err := buildRef.CallInternal(thread, nil, []starlark.Tuple{
			{starlark.String("party_id"), starlark.String("party-123")},
			{starlark.String("org_id"), starlark.String("org-456")},
			{starlark.String("currency"), starlark.String("GBP")},
		})

		require.NoError(t, err)
		assert.Equal(t, `"party:party-123:org:org-456:currency:GBP"`, result.String())
	})

	t.Run("rejects empty party_id", func(t *testing.T) {
		builtins := NewRestrictedBuiltins(nil)
		buildRef := builtins["build_org_account_ref"].(*starlark.Builtin)

		thread := &starlark.Thread{Name: "test"}
		_, err := buildRef.CallInternal(thread, nil, []starlark.Tuple{
			{starlark.String("party_id"), starlark.String("")},
			{starlark.String("org_id"), starlark.String("org-456")},
			{starlark.String("currency"), starlark.String("GBP")},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyParam)
		assert.Contains(t, err.Error(), "party_id")
	})

	t.Run("rejects empty org_id", func(t *testing.T) {
		builtins := NewRestrictedBuiltins(nil)
		buildRef := builtins["build_org_account_ref"].(*starlark.Builtin)

		thread := &starlark.Thread{Name: "test"}
		_, err := buildRef.CallInternal(thread, nil, []starlark.Tuple{
			{starlark.String("party_id"), starlark.String("party-123")},
			{starlark.String("org_id"), starlark.String("")},
			{starlark.String("currency"), starlark.String("GBP")},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyParam)
		assert.Contains(t, err.Error(), "org_id")
	})

	t.Run("rejects empty currency", func(t *testing.T) {
		builtins := NewRestrictedBuiltins(nil)
		buildRef := builtins["build_org_account_ref"].(*starlark.Builtin)

		thread := &starlark.Thread{Name: "test"}
		_, err := buildRef.CallInternal(thread, nil, []starlark.Tuple{
			{starlark.String("party_id"), starlark.String("party-123")},
			{starlark.String("org_id"), starlark.String("org-456")},
			{starlark.String("currency"), starlark.String("")},
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyParam)
		assert.Contains(t, err.Error(), "currency")
	})

	t.Run("missing required parameter", func(t *testing.T) {
		builtins := NewRestrictedBuiltins(nil)
		buildRef := builtins["build_org_account_ref"].(*starlark.Builtin)

		thread := &starlark.Thread{Name: "test"}
		_, err := buildRef.CallInternal(thread, nil, []starlark.Tuple{
			{starlark.String("party_id"), starlark.String("party-123")},
		})

		require.Error(t, err)
	})
}

func TestResolveAccountBuiltin_CompositeReference(t *testing.T) {
	t.Run("resolves composite reference", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		// Build a composite reference
		ref := BuildCompositeAccountRef("party-123", "org-456", "GBP")
		args := starlark.Tuple{starlark.String(ref)}
		result, err := resolveAccount.CallInternal(thread, args, nil)

		require.NoError(t, err)
		// The mock returns based on the reference string
		assert.NotEmpty(t, result.String())
	})

	t.Run("rejects malformed composite reference", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		// Malformed: wrong segment count
		args := starlark.Tuple{starlark.String("party:123:org:456")}
		_, err := resolveAccount.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	})

	t.Run("backward compatible with simple references", func(t *testing.T) {
		mockClient := newMockReferenceDataClient()
		thread := &starlark.Thread{Name: "test"}
		ctx := &StarlarkContext{
			Context:     context.Background(),
			KnowledgeAt: time.Now(),
			LookupCache: NewLookupResultCache(),
		}
		thread.SetLocal("saga.StarlarkContext", ctx)
		thread.SetLocal("saga.ReferenceDataClient", mockClient)

		builtins := NewRestrictedBuiltins(nil)
		resolveAccount := builtins["resolve_account"].(*starlark.Builtin)

		// Simple reference without colons - should work as before
		args := starlark.Tuple{starlark.String("customer-001")}
		result, err := resolveAccount.CallInternal(thread, args, nil)

		require.NoError(t, err)
		assert.Equal(t, `"account-123"`, result.String())
	})
}
