package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func testSchemaRegistry(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	err := reg.LoadFromYAML([]byte(`
service: test
version: "1.0"
handlers:
  test_service.do_thing:
    description: "A test handler"
    compensation_strategy: none
    params:
      account_id:
        type: string
        required: true
      amount:
        type: Decimal
        required: true
      direction:
        type: enum
        values: [DEBIT, CREDIT]
        required: true
      note:
        type: string
        required: false
    returns:
      log_id:
        type: string
      status:
        type: string
  test_service.cancel_thing:
    description: "Cancel handler"
    compensation_strategy: none
    params:
      log_id:
        type: string
        required: true
    returns:
      status:
        type: string
`))
	require.NoError(t, err)
	return reg
}

func execStarlark(t *testing.T, modules starlark.StringDict, script string) error {
	t.Helper()
	predeclared := make(starlark.StringDict)
	for k, v := range modules {
		predeclared[k] = v
	}
	// Add Decimal builtin
	predeclared["Decimal"] = starlark.NewBuiltin("Decimal",
		func(_ *starlark.Thread, _ *starlark.Builtin, _ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
			return starlark.String("0"), nil
		})

	thread := &starlark.Thread{Name: "test"}
	fileOpts := &syntax.FileOptions{}
	_, err := starlark.ExecFileOptions(fileOpts, thread, "test.star", script, predeclared)
	return err
}

func TestBuildValidationModules_ValidCall(t *testing.T) {
	reg := testSchemaRegistry(t)
	var callLog []HandlerCallInfo
	modules, err := BuildValidationModules(reg, &callLog)
	require.NoError(t, err)

	script := `
result = test_service.do_thing(
    account_id="123",
    amount=Decimal("100.00"),
    direction="CREDIT",
)
`
	err = execStarlark(t, modules, script)
	assert.NoError(t, err)

	// Verify call was logged
	require.Len(t, callLog, 1)
	assert.Equal(t, "test_service.do_thing", callLog[0].HandlerName)
	assert.ElementsMatch(t, []string{"account_id", "amount", "direction"}, callLog[0].ParamNames)
}

func TestBuildValidationModules_UnknownParam(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	script := `
result = test_service.do_thing(
    account_id="123",
    amont=Decimal("100.00"),
    direction="CREDIT",
)
`
	err = execStarlark(t, modules, script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNKNOWN_PARAM")
	assert.Contains(t, err.Error(), "amont")
	assert.Contains(t, err.Error(), `"amount"`)
}

func TestBuildValidationModules_MissingRequiredParam(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// Missing required 'amount' and 'direction'
	script := `
result = test_service.do_thing(
    account_id="123",
)
`
	err = execStarlark(t, modules, script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MISSING_REQUIRED_PARAM")
}

func TestBuildValidationModules_WrongParamType(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// account_id expects string, give it an int
	script := `
result = test_service.do_thing(
    account_id=123,
    amount=Decimal("100.00"),
    direction="CREDIT",
)
`
	err = execStarlark(t, modules, script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONG_PARAM_TYPE")
	assert.Contains(t, err.Error(), "account_id")
}

func TestBuildValidationModules_OptionalParamOmitted(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// 'note' is optional - should pass without it
	script := `
result = test_service.do_thing(
    account_id="123",
    amount=Decimal("100.00"),
    direction="CREDIT",
)
`
	err = execStarlark(t, modules, script)
	assert.NoError(t, err)
}

func TestBuildValidationModules_ResultFields(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// Access result fields
	script := `
result = test_service.do_thing(
    account_id="123",
    amount=Decimal("100.00"),
    direction="CREDIT",
)
log_id = result.log_id
status = result.status
`
	err = execStarlark(t, modules, script)
	assert.NoError(t, err)
}

func TestBuildValidationModules_PositionalArgsFail(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	script := `
result = test_service.do_thing("123", Decimal("100.00"), "CREDIT")
`
	err = execStarlark(t, modules, script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positional arguments not allowed")
}

func TestBuildValidationModules_MultipleCallsLogged(t *testing.T) {
	reg := testSchemaRegistry(t)
	var callLog []HandlerCallInfo
	modules, err := BuildValidationModules(reg, &callLog)
	require.NoError(t, err)

	script := `
r1 = test_service.do_thing(
    account_id="123",
    amount=Decimal("100.00"),
    direction="CREDIT",
)
r2 = test_service.cancel_thing(log_id="abc")
`
	err = execStarlark(t, modules, script)
	assert.NoError(t, err)
	assert.Len(t, callLog, 2)
	assert.Equal(t, "test_service.do_thing", callLog[0].HandlerName)
	assert.Equal(t, "test_service.cancel_thing", callLog[1].HandlerName)
}

func TestBuildValidationModules_DefaultRegistry(t *testing.T) {
	reg, err := DefaultRegistry()
	require.NoError(t, err)

	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// Verify that position_keeping module exists
	_, ok := modules["position_keeping"]
	assert.True(t, ok, "position_keeping module should exist")

	// Verify that financial_accounting module exists
	_, ok = modules["financial_accounting"]
	assert.True(t, ok, "financial_accounting module should exist")
}

func TestBuildValidationModules_InvalidEnumValue(t *testing.T) {
	reg := testSchemaRegistry(t)
	modules, err := BuildValidationModules(reg, nil)
	require.NoError(t, err)

	// direction expects DEBIT or CREDIT, give it SIDEWAYS
	script := `
result = test_service.do_thing(
    account_id="123",
    amount=Decimal("100.00"),
    direction="SIDEWAYS",
)
`
	err = execStarlark(t, modules, script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONG_PARAM_TYPE")
	assert.Contains(t, err.Error(), "SIDEWAYS")
	assert.Contains(t, err.Error(), "DEBIT")
}

func TestValidationFailure_ErrorInterface(t *testing.T) {
	vf := &ValidationFailure{
		Code:    ValidationCodeUnknownParam,
		Message: "handler test.foo has no parameter \"bar\"",
	}
	assert.Contains(t, vf.Error(), "UNKNOWN_PARAM")
	assert.Contains(t, vf.Error(), "bar")

	vf.Suggestion = `Did you mean "baz"?`
	assert.Contains(t, vf.Error(), "suggestion")
}
