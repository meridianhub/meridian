package cel_test

import (
	"errors"
	"testing"

	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewCompiler re-export
// ---------------------------------------------------------------------------

func TestNewCompiler_ReturnsCompilerWithoutError(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)
	assert.NotNil(t, compiler)
}

func TestNewCompiler_IsAliasForSharedCompiler(t *testing.T) {
	// Both constructors should succeed and produce working compilers.
	refCompiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	sharedCompiler, err := sharedcel.NewCompiler()
	require.NoError(t, err)

	// Both compilers should compile valid expressions without error.
	_, err = refCompiler.CompileValidation(`parse_decimal(amount) > 0.0`)
	assert.NoError(t, err)

	_, err = sharedCompiler.CompileValidation(`parse_decimal(amount) > 0.0`)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Constant re-exports
// ---------------------------------------------------------------------------

func TestConstants_AreReExported(t *testing.T) {
	assert.Equal(t, sharedcel.CELVersion, refcel.CELVersion)
	assert.Equal(t, sharedcel.MaxExpressionLength, refcel.MaxExpressionLength)
	assert.Equal(t, sharedcel.MaxExpressionDepth, refcel.MaxExpressionDepth)
	assert.Equal(t, sharedcel.CostLimit, refcel.CostLimit)
}

// ---------------------------------------------------------------------------
// Error variable re-exports
// ---------------------------------------------------------------------------

func TestErrors_AreReExported(t *testing.T) {
	assert.True(t, errors.Is(refcel.ErrExpressionTooLong, sharedcel.ErrExpressionTooLong))
	assert.True(t, errors.Is(refcel.ErrExpressionTooDeep, sharedcel.ErrExpressionTooDeep))
	assert.True(t, errors.Is(refcel.ErrEnvironmentCreation, sharedcel.ErrEnvironmentCreation))
	assert.True(t, errors.Is(refcel.ErrCompilation, sharedcel.ErrCompilation))
	assert.True(t, errors.Is(refcel.ErrEligibilityNotBool, sharedcel.ErrEligibilityNotBool))
	assert.True(t, errors.Is(refcel.ErrBucketKeyNotString, sharedcel.ErrBucketKeyNotString))
	assert.True(t, errors.Is(refcel.ErrValidationNotBool, sharedcel.ErrValidationNotBool))
}

// ---------------------------------------------------------------------------
// CompileValidation via re-exported Compiler type
// ---------------------------------------------------------------------------

func TestCompiler_ValidatesValidExpression(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	_, err = compiler.CompileValidation(`parse_decimal(amount) > 0.0`)
	assert.NoError(t, err)
}

func TestCompiler_RejectsInvalidExpression(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	_, err = compiler.CompileValidation(`undefined_variable_xyz > 0`)
	assert.Error(t, err)
}

func TestCompiler_CompileEligibility_ValidExpression(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	_, err = compiler.CompileEligibility(`party.status == 'ACTIVE'`)
	assert.NoError(t, err)
}

func TestCompiler_CompileBucketKey_ValidExpression(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	_, err = compiler.CompileBucketKey(`bucket_key([attributes["type"]])`)
	assert.NoError(t, err)
}
