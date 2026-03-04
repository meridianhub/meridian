package cel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompileEventFilter_ChainDepthVariable verifies that the chain_depth int variable
// is available in the event filter CEL environment.
func TestCompileEventFilter_ChainDepthVariable(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantErr    bool
	}{
		{
			name:       "chain_depth == 0 filter",
			expression: `chain_depth == 0`,
		},
		{
			name:       "chain_depth < 5 filter",
			expression: `chain_depth < 5`,
		},
		{
			name:       "chain_depth combined with event field",
			expression: `chain_depth == 0 && event.type == "PAYMENT"`,
		},
		{
			name:       "chain_depth integer comparison",
			expression: `chain_depth >= 1 && chain_depth <= 10`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileEventFilter(tt.expression)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, prg)
			}
		})
	}
}

// TestCompileEventFilter_ChainDepthEvaluation verifies that chain_depth is correctly
// evaluated in event filter expressions.
func TestCompileEventFilter_ChainDepthEvaluation(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		chainDepth int
		wantMatch  bool
	}{
		{
			name:       "depth 0 matches chain_depth == 0",
			expression: `chain_depth == 0`,
			chainDepth: 0,
			wantMatch:  true,
		},
		{
			name:       "depth 1 does not match chain_depth == 0",
			expression: `chain_depth == 0`,
			chainDepth: 1,
			wantMatch:  false,
		},
		{
			name:       "depth 3 matches chain_depth < 5",
			expression: `chain_depth < 5`,
			chainDepth: 3,
			wantMatch:  true,
		},
		{
			name:       "depth 5 does not match chain_depth < 5",
			expression: `chain_depth < 5`,
			chainDepth: 5,
			wantMatch:  false,
		},
		{
			name:       "depth 0 matches combined filter",
			expression: `chain_depth == 0 && event.type == "PAYMENT"`,
			chainDepth: 0,
			wantMatch:  true,
		},
		{
			name:       "depth 1 does not match combined filter requiring depth 0",
			expression: `chain_depth == 0 && event.type == "PAYMENT"`,
			chainDepth: 1,
			wantMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileEventFilter(tt.expression)
			require.NoError(t, err)

			result, _, err := prg.Eval(map[string]any{
				"event":       map[string]any{"type": "PAYMENT", "amount": 500},
				"metadata":    map[string]string{"source": "bank"},
				"chain_depth": int64(tt.chainDepth),
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantMatch, result.Value())
		})
	}
}

// TestCompileEventFilter_ChainDepthNotInValidationEnv ensures chain_depth is scoped
// only to the event_filter environment and not available in validation expressions.
func TestCompileEventFilter_ChainDepthNotInValidationEnv(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	_, err = c.CompileValidation(`chain_depth == 0`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undeclared reference")
}
