package schema

import (
	"math/big"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func TestStarlarkToGoValue_DictWithNestedValues(t *testing.T) {
	dict := starlark.NewDict(2)
	require.NoError(t, dict.SetKey(starlark.String("nested"), starlark.String("val")))
	innerList := starlark.NewList([]starlark.Value{starlark.MakeInt(1)})
	require.NoError(t, dict.SetKey(starlark.String("list"), innerList))

	val, err := starlarkToGoValue(dict)
	require.NoError(t, err)
	m, ok := val.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "val", m["nested"])
	innerSlice, ok := m["list"].([]any)
	require.True(t, ok)
	assert.Equal(t, int64(1), innerSlice[0])
}

func TestStarlarkToGoValue_StructWithNestedTypes(t *testing.T) {
	inner := starlarkstruct.FromStringDict(starlark.String("inner"), starlark.StringDict{
		"deep": starlark.String("value"),
	})
	outer := starlarkstruct.FromStringDict(starlark.String("outer"), starlark.StringDict{
		"child": inner,
		"num":   starlark.Float(3.14),
	})
	val, err := starlarkToGoValue(outer)
	require.NoError(t, err)
	m, ok := val.(map[string]any)
	require.True(t, ok)
	childMap, ok := m["child"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "value", childMap["deep"])
	assert.InDelta(t, 3.14, m["num"], 0.001)
}

func TestStarlarkIntToGo_VeryLargeInt(t *testing.T) {
	bigInt := new(big.Int).SetUint64(1)
	bigInt.Lsh(bigInt, 128)
	val := starlarkIntToGo(starlark.MakeBigInt(bigInt))
	s, ok := val.(string)
	require.True(t, ok)
	assert.Contains(t, s, "3402823669209384")
}

func TestGoToStarlarkResult_VariousNonMapTypes(t *testing.T) {
	t.Run("boolean result", func(t *testing.T) {
		val, err := goToStarlarkResult("test.handler", true)
		require.NoError(t, err)
		assert.Equal(t, starlark.Bool(true), val)
	})

	t.Run("decimal result", func(t *testing.T) {
		d := decimal.NewFromFloat(99.99)
		val, err := goToStarlarkResult("test.handler", d)
		require.NoError(t, err)
		assert.Equal(t, starlark.String(d.String()), val)
	})

	t.Run("slice result", func(t *testing.T) {
		val, err := goToStarlarkResult("test.handler", []any{"a", "b"})
		require.NoError(t, err)
		list, ok := val.(*starlark.List)
		require.True(t, ok)
		assert.Equal(t, 2, list.Len())
	})
}

func TestGoToStarlarkValue_EmptyCollections(t *testing.T) {
	t.Run("empty map", func(t *testing.T) {
		val, err := goToStarlarkValue(map[string]any{})
		require.NoError(t, err)
		dict, ok := val.(*starlark.Dict)
		require.True(t, ok)
		assert.Equal(t, 0, dict.Len())
	})

	t.Run("empty slice", func(t *testing.T) {
		val, err := goToStarlarkValue([]any{})
		require.NoError(t, err)
		list, ok := val.(*starlark.List)
		require.True(t, ok)
		assert.Equal(t, 0, list.Len())
	})
}

func TestBuildServiceModulesFromSchema_HandlerNotInRegistry(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	s := &Schema{
		Handlers: map[string]*HandlerDef{
			"svc.missing": {
				Params:  map[string]*FieldDef{},
				Returns: map[string]*FieldDef{},
			},
		},
	}
	_, err := BuildServiceModulesFromSchema(registry, s)
	require.Error(t, err)
}

func TestBuildServiceModulesFromSchema_BothRBACFieldsSet(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	registry.Register("svc.action", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	})
	s := &Schema{
		Handlers: map[string]*HandlerDef{
			"svc.action": {
				ResourceType:       "payment_order",
				RequiredPermission: "write",
				Params:             map[string]*FieldDef{},
				Returns:            map[string]*FieldDef{},
			},
		},
	}
	modules, err := BuildServiceModulesFromSchema(registry, s)
	require.NoError(t, err)
	assert.Contains(t, modules, "svc")
}
