package infra

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewCELPreview_EmptyExpression(t *testing.T) {
	p := NewCELPreview("")
	assert.NotNil(t, p)
	assert.False(t, p.IsEnabled())
	assert.Equal(t, "", p.Expression())
}

func TestNewCELPreview_InvalidExpression(t *testing.T) {
	p := NewCELPreview("(((invalid CEL expression @@@")
	assert.NotNil(t, p)
	assert.False(t, p.IsEnabled())
}

func TestNewCELPreview_ValidExpression(t *testing.T) {
	p := NewCELPreview("value != ''")
	assert.NotNil(t, p)
	assert.True(t, p.IsEnabled())
	assert.Equal(t, "value != ''", p.Expression())
}

func TestCELPreview_Evaluate_Disabled(t *testing.T) {
	p := NewCELPreview("")

	result := p.Evaluate("1.0", "DS", "ACTUAL", nil)
	assert.True(t, result.Valid, "disabled preview assumes valid")
	assert.NotEmpty(t, result.Warning)
	assert.Equal(t, 1, p.WarningCount())
}

func TestCELPreview_Evaluate_Passing(t *testing.T) {
	p := NewCELPreview("value != ''")

	result := p.Evaluate("1.0856", "USD_EUR_FX", "ACTUAL", map[string]string{"tenor": "1M"})
	assert.True(t, result.Valid)
	assert.Empty(t, result.Warning)
	assert.Equal(t, 0, p.WarningCount())
}

func TestCELPreview_Evaluate_Failing(t *testing.T) {
	p := NewCELPreview("value == 'EXPECTED_VALUE'")

	result := p.Evaluate("UNEXPECTED", "DS", "ACTUAL", nil)
	assert.False(t, result.Valid)
	assert.NotEmpty(t, result.Warning)
	assert.Equal(t, 1, p.WarningCount())
}

func TestCELPreview_Evaluate_EvalError(t *testing.T) {
	// Expression is valid but will panic at eval due to type issues
	// Use a division by zero scenario in string context - CEL won't error here
	// Instead use an expression that evaluates to non-boolean
	p := NewCELPreview("value + dataset_code") // returns string, not bool

	result := p.Evaluate("a", "b", "ACTUAL", nil)
	assert.True(t, result.Valid, "non-boolean result assumes valid with warning")
	assert.NotEmpty(t, result.Warning)
}

func TestCELPreview_WarningCount_Accumulates(t *testing.T) {
	p := NewCELPreview("value == 'PASS'")

	p.Evaluate("FAIL", "DS", "ACTUAL", nil)
	p.Evaluate("FAIL", "DS", "ACTUAL", nil)
	p.Evaluate("PASS", "DS", "ACTUAL", nil) // this one passes - no warning

	assert.Equal(t, 2, p.WarningCount())
}

func TestCELPreview_Evaluate_AttributesAccessible(t *testing.T) {
	p := NewCELPreview("attributes['tenor'] != ''")

	result := p.Evaluate("1.0", "DS", "ACTUAL", map[string]string{"tenor": "3M"})
	assert.True(t, result.Valid)
}

func TestCELPreview_Expression(t *testing.T) {
	expr := "value != '' && quality_level == 'ACTUAL'"
	p := NewCELPreview(expr)
	assert.Equal(t, expr, p.Expression())
}
