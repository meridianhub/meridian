package valuation_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/valuation"
)

func TestPolicyRuntime_CompilePolicy_Success(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
	}{
		{
			name:       "simple arithmetic",
			expression: "amount * 1.5",
		},
		{
			name:       "conditional expression",
			expression: "tier == 'Gold' ? amount * 0.9 : amount",
		},
		{
			name:       "map access",
			expression: "tariffs['peak'] * kwh",
		},
		{
			name:       "ternary expression",
			expression: "base_rate > volume_rate * quantity ? base_rate : volume_rate * quantity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := runtime.CompilePolicy(tt.expression)
			require.NoError(t, err)
			assert.NotNil(t, policy)
			assert.Equal(t, tt.expression, policy.Expression())
			assert.LessOrEqual(t, policy.EstimatedCost(), uint64(10000), "cost should be within limit")
		})
	}
}

func TestPolicyRuntime_CompilePolicy_ExpressionTooLong(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	// Create expression > 4KB
	longExpr := "1"
	for i := 0; i < 5000; i++ {
		longExpr += " + 1"
	}

	_, err = runtime.CompilePolicy(longExpr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestPolicyRuntime_CompilePolicy_ExpressionTooDeep(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	// Create deeply nested expression (> 10 levels)
	expr := "1"
	for i := 0; i < 15; i++ {
		expr = "(" + expr + ")"
	}

	_, err = runtime.CompilePolicy(expr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum nesting depth")
}

func TestPolicyRuntime_CompilePolicy_InvalidSyntax(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		errMsg     string
	}{
		{
			name:       "unclosed parenthesis",
			expression: "(amount * 1.5",
			errMsg:     "compilation failed",
		},
		{
			name:       "invalid operator",
			expression: "amount ** 2", // ** not supported in CEL
			errMsg:     "compilation failed",
		},
		{
			name:       "undefined variable",
			expression: "undefined_var * 2",
			errMsg:     "compilation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runtime.CompilePolicy(tt.expression)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestPolicyRuntime_EvaluatePolicy_Success(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		inputs     map[string]interface{}
		want       interface{}
	}{
		{
			name:       "simple multiplication",
			expression: "amount * rate",
			inputs: map[string]interface{}{
				"amount": 100.0,
				"rate":   1.5,
			},
			want: 150.0,
		},
		{
			name:       "conditional tier pricing",
			expression: "tier == 'Gold' ? amount * 0.9 : amount",
			inputs: map[string]interface{}{
				"amount": 100.0,
				"tier":   "Gold",
			},
			want: 90.0,
		},
		{
			name:       "map lookup",
			expression: "tariffs[period]",
			inputs: map[string]interface{}{
				"tariffs": map[string]interface{}{
					"peak":     0.35,
					"off-peak": 0.15,
				},
				"period": "peak",
			},
			want: 0.35,
		},
		{
			name:       "boolean expression",
			expression: "amount > 1000.0 && tier == 'Premium'",
			inputs: map[string]interface{}{
				"amount": 1500.0,
				"tier":   "Premium",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := runtime.CompilePolicy(tt.expression)
			require.NoError(t, err)

			result, cost, err := runtime.EvaluatePolicy(context.Background(), policy, tt.inputs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
			assert.Greater(t, cost, uint64(0), "cost should be tracked")
			assert.LessOrEqual(t, cost, uint64(10000), "cost should be within limit")
		})
	}
}

func TestPolicyRuntime_EvaluatePolicy_MissingInput(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	policy, err := runtime.CompilePolicy("amount * rate")
	require.NoError(t, err)

	// Missing 'rate' input
	_, _, err = runtime.EvaluatePolicy(context.Background(), policy, map[string]interface{}{
		"amount": 100.0,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "evaluation failed")
}

func TestPolicyRuntime_EvaluatePolicy_TypeMismatch(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	policy, err := runtime.CompilePolicy("amount * rate")
	require.NoError(t, err)

	// 'rate' should be numeric, not string
	_, _, err = runtime.EvaluatePolicy(context.Background(), policy, map[string]interface{}{
		"amount": 100.0,
		"rate":   "invalid",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "evaluation failed")
}

func TestPolicyRuntime_CostLimit(t *testing.T) {
	runtime, err := valuation.NewPolicyRuntime()
	require.NoError(t, err)

	// Create expression with high cost (many operations)
	expr := "1"
	for i := 0; i < 100; i++ {
		expr += " + 1"
	}

	policy, err := runtime.CompilePolicy(expr)
	require.NoError(t, err)

	result, cost, err := runtime.EvaluatePolicy(context.Background(), policy, map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, int64(101), result) // 1 + 1 + 1 + ... (101 times)
	assert.Greater(t, cost, uint64(0))
	assert.LessOrEqual(t, cost, uint64(10000), "cost should be within 10k limit")
}
