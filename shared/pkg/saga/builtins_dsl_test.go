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

	t.Run("handles missing context", func(t *testing.T) {
		thread := &starlark.Thread{Name: "test"}
		// Don't set StarlarkContext

		builtins := NewRestrictedBuiltins(nil)
		celEval := builtins["cel_eval"].(*starlark.Builtin)

		args := starlark.Tuple{starlark.String("1 + 1")}
		_, err := celEval.CallInternal(thread, args, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "StarlarkContext not found")
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
		assert.Contains(t, err.Error(), "client not configured")
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
			want:  int64(42),
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
}
