package validator

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

func TestPermissiveServiceStub_Interface(t *testing.T) {
	stub := newPermissiveServiceStub("position_keeping")

	assert.Equal(t, "<service position_keeping>", stub.String())
	assert.Equal(t, "service", stub.Type())
	assert.True(t, bool(stub.Truth()))
	assert.Nil(t, stub.AttrNames())

	_, hashErr := stub.Hash()
	assert.ErrorIs(t, hashErr, ErrUnhashable)

	// Attr returns a callable
	val, err := stub.Attr("initiate_log")
	assert.NoError(t, err)
	assert.NotNil(t, val)
}

func TestPermissiveServiceStub_AttrCallable(t *testing.T) {
	stub := newPermissiveServiceStub("position_keeping")

	attr, err := stub.Attr("some_handler")
	require.NoError(t, err)

	builtin, ok := attr.(*starlark.Builtin)
	require.True(t, ok)

	// Should be callable and return a permissive result
	thread := &starlark.Thread{Name: "test"}
	result, callErr := starlark.Call(thread, builtin, nil, nil)
	assert.NoError(t, callErr)
	assert.NotNil(t, result)
}

func TestPermissiveResult_Interface(t *testing.T) {
	r := &permissiveResult{}

	assert.Equal(t, "<result>", r.String())
	assert.Equal(t, "result", r.Type())
	assert.True(t, bool(r.Truth()))
	assert.Nil(t, r.AttrNames())

	_, hashErr := r.Hash()
	assert.ErrorIs(t, hashErr, ErrUnhashable)

	// Attr always returns another permissive result
	val, err := r.Attr("anything")
	assert.NoError(t, err)
	assert.NotNil(t, val)
	_, ok := val.(*permissiveResult)
	assert.True(t, ok)
}

func TestErrUnhashable(t *testing.T) {
	assert.EqualError(t, ErrUnhashable, "unhashable: module")
}

func TestValidateStarlarkScripts_ValidScript(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "process_order",
				Trigger: "api:/v1/orders",
				Script:  "def execute(ctx):\n    return {\"status\": \"ok\"}\n",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateStarlarkScripts_SyntaxError(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "bad_saga",
				Trigger: "api:/v1/bad",
				Script:  "def execute(ctx):\n    return {{{invalid",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	require.NotEmpty(t, result.Errors)
	assert.Equal(t, CodeStarlarkSyntaxError, result.Errors[0].Code)
}

func TestValidateStarlarkScripts_WhileLoopRejected(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Starlark forbids while loops - they don't compile
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "infinite_saga",
				Trigger: "api:/v1/infinite",
				Script:  "def execute(ctx):\n    while True:\n        pass\n",
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	require.NotEmpty(t, result.Errors)
	assert.Equal(t, CodeStarlarkSyntaxError, result.Errors[0].Code)
}

func TestValidateStarlarkScripts_ValidForLoop(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "loop_saga",
				Trigger: "api:/v1/loop",
				Script: `def execute(ctx):
    items = [1, 2, 3]
    total = 0
    for item in items:
        total = total + item
    return {"total": total}
`,
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	assert.Empty(t, result.Errors)
}

func TestValidateStarlarkScripts_ScriptTooLarge(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Build a script that exceeds the size limit (65536 bytes)
	const maxSize = 65536
	bigScript := make([]byte, maxSize+1)
	for i := range bigScript {
		bigScript[i] = '#' // comment character
	}

	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "huge_saga",
				Trigger: "api:/v1/huge",
				Script:  string(bigScript),
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	require.NotEmpty(t, result.Errors)
	assert.Contains(t, result.Errors[0].Code, "STARLARK")
}

func TestValidateStarlarkScripts_KnownServiceBinding(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// Use a known service binding (position_keeping)
	manifest := &controlplanev1.Manifest{
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "log_position",
				Trigger: "api:/v1/log",
				Script: `def execute(ctx):
    result = ctx.position_keeping.initiate_log(
        position_id="test",
        amount=Decimal("100"),
        direction="CREDIT",
    )
    return {"done": True}
`,
			},
		},
	}

	result := &ValidationResult{Valid: true}
	v.validateStarlarkScripts(manifest, result)
	// Without a schema registry, permissive stubs allow any call
	assert.Empty(t, result.Errors)
}

func TestCodeConstants(t *testing.T) {
	assert.Equal(t, "STARLARK_SYNTAX_ERROR", CodeStarlarkSyntaxError)
	assert.Equal(t, "STARLARK_COMPILATION_ERROR", CodeStarlarkCompilationError)
}
