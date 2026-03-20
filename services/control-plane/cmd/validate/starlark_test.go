package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// --- collectStarlarkFiles ---

func TestCollectStarlarkFiles_SimpleGlob(t *testing.T) {
	dir := t.TempDir()
	// Create .star files
	for _, name := range []string{"a.star", "b.star", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x = 1"), 0o644))
	}

	files, err := collectStarlarkFiles(filepath.Join(dir, "*.star"))
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestCollectStarlarkFiles_DoubleStarGlob(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "top.star"), []byte("x = 1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested.star"), []byte("x = 1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "other.txt"), []byte("x = 1"), 0o644))

	files, err := collectStarlarkFiles(filepath.Join(dir, "**", "*.star"))
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestCollectStarlarkFiles_NoMatches(t *testing.T) {
	dir := t.TempDir()
	files, err := collectStarlarkFiles(filepath.Join(dir, "*.star"))
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestCollectStarlarkFiles_InvalidRoot(t *testing.T) {
	_, err := collectStarlarkFiles("/nonexistent/path/**/*.star")
	assert.Error(t, err)
}

// --- hasSkipDirective ---

func TestHasSkipDirective(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"present at top", "# schema-validation: skip\nx = 1", true},
		{"present in middle", "x = 1\n# schema-validation: skip\ny = 2", true},
		{"present with leading whitespace", "  # schema-validation: skip  \nx = 1", true},
		{"absent", "x = 1\n# some other comment", false},
		{"inside string literal check", `x = "# schema-validation: skip"`, false},
		{"empty content", "", false},
		{"partial match", "# schema-validation: ski", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasSkipDirective(tt.content))
		})
	}
}

// --- decimalBuiltin ---

func TestDecimalBuiltin_Float(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{starlark.Float(3.14)}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.Float(3.14), result)
}

func TestDecimalBuiltin_Int(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{starlark.MakeInt(42)}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.Float(42.0), result)
}

func TestDecimalBuiltin_String(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{starlark.String("99.5")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.Float(99.5), result)
}

func TestDecimalBuiltin_InvalidString(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	_, err := fn.CallInternal(thread, starlark.Tuple{starlark.String("not-a-number")}, nil)
	assert.ErrorIs(t, err, errDecimalParse)
}

func TestDecimalBuiltin_UnsupportedType(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	_, err := fn.CallInternal(thread, starlark.Tuple{starlark.True}, nil)
	assert.ErrorIs(t, err, errDecimalType)
}

func TestDecimalBuiltin_WrongArgCount(t *testing.T) {
	fn := decimalBuiltin()
	thread := &starlark.Thread{Name: "test"}
	_, err := fn.CallInternal(thread, starlark.Tuple{}, nil)
	assert.ErrorIs(t, err, errDecimalArgCount)

	_, err = fn.CallInternal(thread, starlark.Tuple{starlark.Float(1), starlark.Float(2)}, nil)
	assert.ErrorIs(t, err, errDecimalArgCount)
}

// --- permissiveInputDict ---

func TestPermissiveInputDict_StringMethods(t *testing.T) {
	d := &permissiveInputDict{}
	assert.Equal(t, "input_data{}", d.String())
	assert.Equal(t, "dict", d.Type())
	assert.Equal(t, starlark.True, d.Truth())
}

func TestPermissiveInputDict_Hash(t *testing.T) {
	d := &permissiveInputDict{}
	_, err := d.Hash()
	assert.ErrorIs(t, err, errUnhashableDict)
}

func TestPermissiveInputDict_Get_StringKey(t *testing.T) {
	d := &permissiveInputDict{}
	val, found, err := d.Get(starlark.String("name"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, starlark.String("mock-name"), val)
}

func TestPermissiveInputDict_Get_NonStringKey(t *testing.T) {
	d := &permissiveInputDict{}
	val, found, err := d.Get(starlark.MakeInt(42))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, starlark.String(""), val)
}

func TestPermissiveInputDict_Get_NumericFields(t *testing.T) {
	d := &permissiveInputDict{}

	// amount should return Float
	val, _, _ := d.Get(starlark.String("amount"))
	assert.Equal(t, starlark.Float(10.0), val)

	// amount_cents should return Int
	val, _, _ = d.Get(starlark.String("amount_cents"))
	assert.Equal(t, starlark.MakeInt(10), val)
}

func TestPermissiveInputDict_Get_NestedFields(t *testing.T) {
	d := &permissiveInputDict{}
	val, _, _ := d.Get(starlark.String("metadata"))
	_, ok := val.(*permissiveResultValue)
	assert.True(t, ok, "metadata should return permissiveResultValue")
}

func TestPermissiveInputDict_Attr_Get(t *testing.T) {
	d := &permissiveInputDict{}
	getAttr, err := d.Attr("get")
	require.NoError(t, err)
	assert.NotNil(t, getAttr)

	// Call get with default value
	fn := getAttr.(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{starlark.String("key"), starlark.String("default")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("default"), result)
}

func TestPermissiveInputDict_Attr_GetOneArg(t *testing.T) {
	d := &permissiveInputDict{}
	getAttr, _ := d.Attr("get")
	fn := getAttr.(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{starlark.String("name")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("mock-name"), result)
}

func TestPermissiveInputDict_Attr_GetNoArgs(t *testing.T) {
	d := &permissiveInputDict{}
	getAttr, _ := d.Attr("get")
	fn := getAttr.(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, starlark.Tuple{}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String(""), result)
}

func TestPermissiveInputDict_Attr_Other(t *testing.T) {
	d := &permissiveInputDict{}
	val, err := d.Attr("unknown")
	require.NoError(t, err)
	assert.Equal(t, starlark.None, val)
}

func TestPermissiveInputDict_AttrNames(t *testing.T) {
	d := &permissiveInputDict{}
	assert.Equal(t, []string{"get"}, d.AttrNames())
}

func TestPermissiveInputDict_Freeze(_ *testing.T) {
	d := &permissiveInputDict{}
	d.Freeze() // should not panic
}

// --- permissiveValue ---

func TestPermissiveValue(t *testing.T) {
	tests := []struct {
		name     string
		field    string
		wantType string
	}{
		{"int field amount_cents", "amount_cents", "int"},
		{"int field with suffix", "total_minor_units", "int"},
		{"int field count", "count", "int"},
		{"numeric field amount", "amount", "float"},
		{"numeric field balance", "balance", "float"},
		{"numeric field stake_amount", "stake_amount", "float"},
		{"nested field metadata", "metadata", "input_data.metadata"},
		{"nested field attributes", "attributes", "input_data.attributes"},
		{"string field default", "some_field", "string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := permissiveValue(tt.field)
			switch tt.wantType {
			case "int":
				_, ok := val.(starlark.Int)
				assert.True(t, ok, "expected Int for %s", tt.field)
			case "float":
				_, ok := val.(starlark.Float)
				assert.True(t, ok, "expected Float for %s", tt.field)
			case "string":
				s, ok := val.(starlark.String)
				assert.True(t, ok, "expected String for %s", tt.field)
				assert.Equal(t, starlark.String("mock-"+tt.field), s)
			default:
				rv, ok := val.(*permissiveResultValue)
				assert.True(t, ok, "expected permissiveResultValue for %s", tt.field)
				assert.Equal(t, tt.wantType, rv.name)
			}
		})
	}
}

// --- openServiceModule ---

func TestOpenServiceModule_StringMethods(t *testing.T) {
	m := &openServiceModule{name: "test_module"}
	assert.Equal(t, "test_module", m.String())
	assert.Equal(t, "open_service_module", m.Type())
	assert.Equal(t, starlark.True, m.Truth())
}

func TestOpenServiceModule_Hash(t *testing.T) {
	m := &openServiceModule{name: "test"}
	_, err := m.Hash()
	assert.ErrorIs(t, err, errUnhashableModule)
}

func TestOpenServiceModule_Attr(t *testing.T) {
	m := &openServiceModule{name: "svc"}
	val, err := m.Attr("some_handler")
	require.NoError(t, err)

	// Should return a callable builtin
	fn, ok := val.(*starlark.Builtin)
	require.True(t, ok)

	// Call it and verify it returns a permissiveResultValue
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, nil, nil)
	require.NoError(t, err)
	rv, ok := result.(*permissiveResultValue)
	require.True(t, ok)
	assert.Equal(t, "svc.some_handler.Result", rv.name)
}

func TestOpenServiceModule_AttrNames(t *testing.T) {
	m := &openServiceModule{name: "test"}
	assert.Nil(t, m.AttrNames())
}

func TestOpenServiceModule_Freeze(_ *testing.T) {
	m := &openServiceModule{name: "test"}
	m.Freeze() // should not panic
}

// --- hybridServiceModule ---

func TestHybridServiceModule_StringMethods(t *testing.T) {
	h := &hybridServiceModule{name: "hybrid"}
	assert.Equal(t, "hybrid", h.String())
	assert.Equal(t, "hybrid_service_module", h.Type())
	assert.Equal(t, starlark.True, h.Truth())
}

func TestHybridServiceModule_Hash(t *testing.T) {
	h := &hybridServiceModule{name: "hybrid"}
	_, err := h.Hash()
	assert.ErrorIs(t, err, errUnhashableHybrid)
}

func TestHybridServiceModule_Attr_FallbackAllowed(t *testing.T) {
	// strict module that has no attrs
	strict := &openServiceModule{name: "strict"}
	open := &openServiceModule{name: "open"}
	h := &hybridServiceModule{
		name:         "test_svc",
		strict:       strict,
		open:         open,
		openHandlers: map[string]struct{}{"allowed_handler": {}},
	}

	// "allowed_handler" is in the open list - use strict first (which returns a value),
	// but actually for this test we need a strict that doesn't have the attr.
	// The openServiceModule always returns something for Attr, so strict will find it.
	// Let's use a starlark.StringDict instead as strict.
	strictDict := starlark.StringDict{}
	h.strict = &moduleFromDict{dict: strictDict}

	val, err := h.Attr("allowed_handler")
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestHybridServiceModule_Attr_NotAllowed(t *testing.T) {
	strictDict := starlark.StringDict{}
	open := &openServiceModule{name: "open"}
	h := &hybridServiceModule{
		name:         "test_svc",
		strict:       &moduleFromDict{dict: strictDict},
		open:         open,
		openHandlers: map[string]struct{}{"allowed_handler": {}},
	}

	_, err := h.Attr("disallowed_handler")
	assert.ErrorIs(t, err, errHandlerNotAllowed)
}

func TestHybridServiceModule_Attr_StrictHit(t *testing.T) {
	builtinVal := starlark.NewBuiltin("strict_fn", func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.String("strict"), nil
	})
	strictDict := starlark.StringDict{"my_handler": builtinVal}
	open := &openServiceModule{name: "open"}
	h := &hybridServiceModule{
		name:         "test_svc",
		strict:       &moduleFromDict{dict: strictDict},
		open:         open,
		openHandlers: map[string]struct{}{},
	}

	val, err := h.Attr("my_handler")
	require.NoError(t, err)
	assert.Equal(t, builtinVal, val)
}

func TestHybridServiceModule_AttrNames_WithHasAttrs(t *testing.T) {
	strictDict := starlark.StringDict{"handler_a": starlark.None}
	h := &hybridServiceModule{
		name:   "test",
		strict: &moduleFromDict{dict: strictDict},
	}
	names := h.AttrNames()
	assert.Contains(t, names, "handler_a")
}

func TestHybridServiceModule_AttrNames_NonHasAttrs(t *testing.T) {
	// starlark.Int does not implement HasAttrs
	h := &hybridServiceModule{
		name:   "test",
		strict: starlark.MakeInt(0),
	}
	assert.Nil(t, h.AttrNames())
}

func TestHybridServiceModule_Freeze(_ *testing.T) {
	h := &hybridServiceModule{name: "test"}
	h.Freeze() // should not panic
}

// moduleFromDict is a test helper that implements starlark.Value and starlark.HasAttrs.
type moduleFromDict struct {
	dict starlark.StringDict
}

func (m *moduleFromDict) String() string        { return "module" }
func (m *moduleFromDict) Type() string          { return "module" }
func (m *moduleFromDict) Freeze()               {}
func (m *moduleFromDict) Truth() starlark.Bool  { return starlark.True }
func (m *moduleFromDict) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable") }

func (m *moduleFromDict) Attr(name string) (starlark.Value, error) {
	v, ok := m.dict[name]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (m *moduleFromDict) AttrNames() []string {
	names := make([]string, 0, len(m.dict))
	for k := range m.dict {
		names = append(names, k)
	}
	return names
}

// --- permissiveResultValue ---

func TestPermissiveResultValue_StringMethods(t *testing.T) {
	r := &permissiveResultValue{name: "test.Result"}
	assert.Equal(t, "test.Result{}", r.String())
	assert.Equal(t, "test.Result", r.Type())
	assert.Equal(t, starlark.True, r.Truth())
}

func TestPermissiveResultValue_Hash(t *testing.T) {
	r := &permissiveResultValue{name: "test"}
	_, err := r.Hash()
	assert.ErrorIs(t, err, errUnhashableResult)
}

func TestPermissiveResultValue_Attr_Get(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	getAttr, err := r.Attr("get")
	require.NoError(t, err)

	fn := getAttr.(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}

	// With default
	val, err := fn.CallInternal(thread, starlark.Tuple{starlark.String("key"), starlark.String("default")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("default"), val)

	// Without default
	val, err = fn.CallInternal(thread, starlark.Tuple{starlark.String("key")}, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.String(""), val)
}

func TestPermissiveResultValue_Attr_Other(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	val, err := r.Attr("status")
	require.NoError(t, err)
	// Should return a permissiveResultField value
	assert.NotNil(t, val)
}

func TestPermissiveResultValue_AttrNames(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	assert.Nil(t, r.AttrNames())
}

func TestPermissiveResultValue_Get_StringKey(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	val, found, err := r.Get(starlark.String("status"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, val)
}

func TestPermissiveResultValue_Get_NonStringKey(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	val, found, err := r.Get(starlark.MakeInt(0))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, starlark.String(""), val)
}

func TestPermissiveResultValue_Len(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	assert.Equal(t, 2, r.Len())
}

func TestPermissiveResultValue_Index(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	val := r.Index(0)
	rv, ok := val.(*permissiveResultValue)
	require.True(t, ok)
	assert.Equal(t, "result[0]", rv.name)
}

func TestPermissiveResultValue_Iterate(t *testing.T) {
	r := &permissiveResultValue{name: "result"}
	iter := r.Iterate()
	defer iter.Done()

	var items []starlark.Value
	var val starlark.Value
	for iter.Next(&val) {
		items = append(items, val)
	}
	assert.Len(t, items, 2)
}

func TestPermissiveResultValue_Freeze(_ *testing.T) {
	r := &permissiveResultValue{name: "test"}
	r.Freeze() // should not panic
}

// --- resultIterator ---

func TestResultIterator_Empty(t *testing.T) {
	iter := &resultIterator{items: nil}
	var val starlark.Value
	assert.False(t, iter.Next(&val))
	iter.Done() // should not panic
}

func TestResultIterator_Exhaustion(t *testing.T) {
	iter := &resultIterator{
		items: []starlark.Value{starlark.String("a"), starlark.String("b")},
	}
	var val starlark.Value

	assert.True(t, iter.Next(&val))
	assert.Equal(t, starlark.String("a"), val)

	assert.True(t, iter.Next(&val))
	assert.Equal(t, starlark.String("b"), val)

	assert.False(t, iter.Next(&val))
}

// --- permissiveResultField ---

func TestPermissiveResultField(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		checkType string
	}{
		{"list field items", "items", "list"},
		{"list field results", "results", "list"},
		{"list field participants", "participants", "list"},
		{"int field count", "count", "int"},
		{"numeric field amount", "amount", "float"},
		{"numeric field balance", "balance", "float"},
		{"nested field metadata", "metadata", "result"},
		{"nested field attributes", "attributes", "result"},
		{"string field default", "some_field", "string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := permissiveResultField("ctx", tt.field)
			switch tt.checkType {
			case "list":
				_, ok := val.(*starlark.List)
				assert.True(t, ok, "expected List for %s", tt.field)
			case "int":
				_, ok := val.(starlark.Int)
				assert.True(t, ok, "expected Int for %s", tt.field)
			case "float":
				_, ok := val.(starlark.Float)
				assert.True(t, ok, "expected Float for %s", tt.field)
			case "result":
				_, ok := val.(*permissiveResultValue)
				assert.True(t, ok, "expected permissiveResultValue for %s", tt.field)
			case "string":
				_, ok := val.(starlark.String)
				assert.True(t, ok, "expected String for %s", tt.field)
			}
		})
	}
}

// --- validateStarlarkFiles ---

func TestValidateStarlarkFiles_NoMatches(t *testing.T) {
	dir := t.TempDir()
	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	_, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	assert.ErrorIs(t, err, errNoFilesMatched)
}

func TestValidateStarlarkFiles_SkipDirective(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "skip.star"),
		[]byte("# schema-validation: skip\nx = 1"),
		0o644,
	))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Skipped)
	assert.True(t, results[0].Pass)
}

func TestValidateStarlarkFiles_ValidScript(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "valid.star"),
		[]byte("x = 1 + 2\ny = x * 3"),
		0o644,
	))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Pass)
	assert.False(t, results[0].Skipped)
}

func TestValidateStarlarkFiles_SyntaxError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "bad.star"),
		[]byte("def foo(\n"),
		0o644,
	))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Pass)
	assert.Contains(t, results[0].Error, "syntax error")
}

func TestValidateStarlarkFiles_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.star")
	require.NoError(t, os.WriteFile(path, []byte("x = 1"), 0o644))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Pass)
	assert.Contains(t, results[0].Error, "failed to read file")
}

// --- validateSingleStarlarkFile ---

func TestValidateSingleStarlarkFile_NonexistentFile(t *testing.T) {
	reg := schema.NewRegistryFromSchema(&schema.Schema{Handlers: map[string]*schema.HandlerDef{}})
	result := validateSingleStarlarkFile("/nonexistent/file.star", reg)
	assert.False(t, result.Pass)
	assert.Contains(t, result.Error, "failed to read file")
}

// --- buildStarlarkPredeclared ---

func TestBuildStarlarkPredeclared_HasExpectedKeys(t *testing.T) {
	reg := schema.NewRegistryFromSchema(&schema.Schema{Handlers: map[string]*schema.HandlerDef{}})
	predeclared, err := buildStarlarkPredeclared(reg)
	require.NoError(t, err)

	// Standard builtins
	assert.Contains(t, predeclared, "saga")
	assert.Contains(t, predeclared, "step")
	assert.Contains(t, predeclared, "Decimal")
	assert.Contains(t, predeclared, "input_data")

	// Cookbook namespaces
	assert.Contains(t, predeclared, "current_account")
	assert.Contains(t, predeclared, "financial_accounting")
	assert.Contains(t, predeclared, "position_keeping")
	assert.Contains(t, predeclared, "reference_data")
	assert.Contains(t, predeclared, "party")
}

func TestBuildStarlarkPredeclared_SagaBuiltin(t *testing.T) {
	reg := schema.NewRegistryFromSchema(&schema.Schema{Handlers: map[string]*schema.HandlerDef{}})
	predeclared, err := buildStarlarkPredeclared(reg)
	require.NoError(t, err)

	fn := predeclared["saga"].(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestBuildStarlarkPredeclared_StepBuiltin(t *testing.T) {
	reg := schema.NewRegistryFromSchema(&schema.Schema{Handlers: map[string]*schema.HandlerDef{}})
	predeclared, err := buildStarlarkPredeclared(reg)
	require.NoError(t, err)

	fn := predeclared["step"].(*starlark.Builtin)
	thread := &starlark.Thread{Name: "test"}
	result, err := fn.CallInternal(thread, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, starlark.None, result)
}

// --- buildDerivedSchema (integration test) ---

func TestBuildDerivedSchema(t *testing.T) {
	derivedSchema, err := buildDerivedSchema()
	require.NoError(t, err)
	assert.NotNil(t, derivedSchema)
	assert.NotEmpty(t, derivedSchema.Handlers)
}

// --- End-to-end Starlark validation with schema ---

func TestValidateStarlarkFiles_WithSchemaModules(t *testing.T) {
	dir := t.TempDir()

	// Write a simple script that uses standard builtins
	script := `
s = saga("test_saga")
step("step1")
d = Decimal("100.50")
val = input_data["amount"]
name = input_data.get("name", "default")
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.star"), []byte(script), 0o644))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Pass, "expected pass, got error: %s", results[0].Error)
}

func TestValidateStarlarkFiles_OpenModuleUsage(t *testing.T) {
	dir := t.TempDir()

	// Write a script that calls an open module handler
	script := `
result = valuation_engine.calculate(amount=Decimal("100"))
status = result.status
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.star"), []byte(script), 0o644))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Pass, "expected pass, got error: %s", results[0].Error)
}

func TestValidateStarlarkFiles_InputDataIteration(t *testing.T) {
	dir := t.TempDir()

	// Test that input_data works with dict access and iteration on results
	script := `
s = saga("iter_test")
step("check_input")
amt = input_data["amount"]
name = input_data["name"]
meta = input_data["metadata"]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.star"), []byte(script), 0o644))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Pass, "expected pass, got error: %s", results[0].Error)
}

func TestValidateStarlarkFiles_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.star"), []byte("x = 1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.star"), []byte("y = 2"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.star"), []byte("def foo(\n"), 0o644))

	s := &schema.Schema{Handlers: map[string]*schema.HandlerDef{}}
	results, err := validateStarlarkFiles(filepath.Join(dir, "*.star"), s)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	passCount := 0
	failCount := 0
	for _, r := range results {
		if r.Pass {
			passCount++
		} else {
			failCount++
		}
	}
	assert.Equal(t, 2, passCount)
	assert.Equal(t, 1, failCount)
}
