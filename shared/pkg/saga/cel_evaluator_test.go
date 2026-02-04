package saga

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCELEvaluator(t *testing.T) {
	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)
	require.NotNil(t, evaluator)
	require.NotNil(t, evaluator.env)
}

func TestCELEvaluator_Eval_SimpleExpressions(t *testing.T) {
	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		variables  map[string]interface{}
		want       interface{}
	}{
		{
			name:       "string concatenation",
			expression: `"hello" + " " + "world"`,
			variables:  map[string]interface{}{},
			want:       "hello world",
		},
		{
			name:       "arithmetic",
			expression: "1 + 1",
			variables:  map[string]interface{}{},
			want:       int64(2),
		},
		{
			name:       "boolean expression",
			expression: "5 > 3",
			variables:  map[string]interface{}{},
			want:       true,
		},
		{
			name:       "input variable access",
			expression: "input.amount > 100",
			variables: map[string]interface{}{
				"input": map[string]interface{}{
					"amount": int64(150),
				},
			},
			want: true,
		},
		{
			name:       "ctx variable access",
			expression: `ctx.saga_execution_id != ""`,
			variables: map[string]interface{}{
				"ctx": map[string]interface{}{
					"saga_execution_id": "test-123",
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.Eval(tt.expression, tt.variables)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestCELEvaluator_Eval_CompilationErrors(t *testing.T) {
	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		variables  map[string]interface{}
	}{
		{
			name:       "invalid syntax",
			expression: "1 ++ 2",
			variables:  map[string]interface{}{},
		},
		{
			name:       "undefined variable",
			expression: "unknown_var",
			variables:  map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := evaluator.Eval(tt.expression, tt.variables)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrCELCompilationFailed)
		})
	}
}

func TestCELEvaluator_Eval_EvaluationErrors(t *testing.T) {
	evaluator, err := NewCELEvaluator()
	require.NoError(t, err)

	t.Run("type mismatch", func(t *testing.T) {
		// This should compile but fail at evaluation
		_, err := evaluator.Eval("input.value + 5", map[string]interface{}{
			"input": map[string]interface{}{
				"value": "string", // String + int should fail
			},
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCELEvaluationFailed)
	})
}

func TestGoToStarlark(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		check func(t *testing.T, result interface{})
	}{
		{
			name:  "nil",
			input: nil,
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "None", result.(fmt.Stringer).String())
			},
		},
		{
			name:  "string",
			input: "test",
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, `"test"`, result.(fmt.Stringer).String())
			},
		},
		{
			name:  "int",
			input: 42,
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "42", result.(fmt.Stringer).String())
			},
		},
		{
			name:  "int64",
			input: int64(42),
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "42", result.(fmt.Stringer).String())
			},
		},
		{
			name:  "float64",
			input: 3.14,
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "3.14", result.(fmt.Stringer).String())
			},
		},
		{
			name:  "bool true",
			input: true,
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "True", result.(fmt.Stringer).String())
			},
		},
		{
			name:  "bool false",
			input: false,
			check: func(t *testing.T, result interface{}) {
				assert.Equal(t, "False", result.(fmt.Stringer).String())
			},
		},
		{
			name: "map",
			input: map[string]interface{}{
				"key": "value",
			},
			check: func(t *testing.T, result interface{}) {
				str := result.(fmt.Stringer).String()
				assert.Contains(t, str, "key")
				assert.Contains(t, str, "value")
			},
		},
		{
			name:  "list",
			input: []interface{}{"a", "b", "c"},
			check: func(t *testing.T, result interface{}) {
				str := result.(fmt.Stringer).String()
				assert.Contains(t, str, "a")
				assert.Contains(t, str, "b")
				assert.Contains(t, str, "c")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := goToStarlark(tt.input)
			require.NotNil(t, result)
			tt.check(t, result)
		})
	}
}
