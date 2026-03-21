package cel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateValidationCEL(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	t.Run("valid expression", func(t *testing.T) {
		err := c.ValidateValidationCEL("amount != ''")
		assert.NoError(t, err)
	})

	t.Run("invalid expression", func(t *testing.T) {
		err := c.ValidateValidationCEL("invalid !! syntax")
		assert.Error(t, err)
	})
}

func TestValidateBucketingCEL(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	t.Run("valid expression", func(t *testing.T) {
		err := c.ValidateBucketingCEL("bucket_key(['METERING'])")
		assert.NoError(t, err)
	})

	t.Run("invalid expression", func(t *testing.T) {
		err := c.ValidateBucketingCEL("!!!bad")
		assert.Error(t, err)
	})
}

func TestValidateEligibilityCEL(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	t.Run("valid expression", func(t *testing.T) {
		err := c.ValidateEligibilityCEL("party.type == 'INDIVIDUAL'")
		assert.NoError(t, err)
	})

	t.Run("invalid expression", func(t *testing.T) {
		err := c.ValidateEligibilityCEL("not valid >><< cel")
		assert.Error(t, err)
	})
}

func TestCompileValueExpression_ValidAndInvalid(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	t.Run("simple expression", func(t *testing.T) {
		prog, err := c.CompileValueExpression("amount")
		assert.NoError(t, err)
		assert.NotNil(t, prog)
	})

	t.Run("string comparison expression", func(t *testing.T) {
		prog, err := c.CompileValueExpression("source")
		assert.NoError(t, err)
		assert.NotNil(t, prog)
	})

	t.Run("invalid expression", func(t *testing.T) {
		_, err := c.CompileValueExpression("!!!bad")
		assert.Error(t, err)
	})
}
