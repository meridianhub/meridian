package cel

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCELVersion(t *testing.T) {
	// CELVersion should match what's in go.mod
	assert.Equal(t, "0.26.1", CELVersion)
}

func TestNewCompiler(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.NotNil(t, c.validationEnv)
	assert.NotNil(t, c.bucketKeyEnv)
}

func TestCompileValidation(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantErr    bool
		errContain string
	}{
		{
			name:       "simple boolean",
			expression: "true",
			wantErr:    false,
		},
		{
			name:       "attribute access",
			expression: `attributes["region"] == "us-east-1"`,
			wantErr:    false,
		},
		{
			name:       "amount comparison with parse",
			expression: `parse_decimal(amount) > 0.0`,
			wantErr:    false,
		},
		{
			name:       "complex validation",
			expression: `source != "" && attributes["type"] in ["A", "B", "C"]`,
			wantErr:    false,
		},
		{
			name:       "timestamp comparison",
			expression: `valid_from < valid_to`,
			wantErr:    false,
		},
		{
			name:       "invalid expression",
			expression: `undefined_variable == "test"`,
			wantErr:    true,
			errContain: "undeclared reference",
		},
		{
			name:       "syntax error",
			expression: `attributes["region" ==`,
			wantErr:    true,
			errContain: "CEL compilation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					assert.Contains(t, err.Error(), tt.errContain)
				}
				assert.Nil(t, prg)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, prg)
			}
		})
	}
}

func TestCompileValidation_Evaluation(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileValidation(`attributes["region"] == "us-east-1" && parse_decimal(amount) > 0.0`)
	require.NoError(t, err)

	now := time.Now()

	result, _, err := prg.Eval(map[string]any{
		"attributes": map[string]string{"region": "us-east-1"},
		"amount":     "100.50",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	})
	require.NoError(t, err)
	assert.Equal(t, true, result.Value())
}

func TestCompileBucketKey(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantErr    bool
		errContain string
	}{
		{
			name:       "simple bucket key",
			expression: `bucket_key(["region", attributes["region"]])`,
			wantErr:    false,
		},
		{
			name:       "multi-attribute key",
			expression: `bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`,
			wantErr:    false,
		},
		{
			name:       "with parse functions",
			expression: `bucket_key([attributes["year"]])`,
			wantErr:    false,
		},
		{
			name:       "invalid variable",
			expression: `bucket_key([amount])`, // amount not in bucket key env
			wantErr:    true,
			errContain: "undeclared reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileBucketKey(tt.expression)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					assert.Contains(t, err.Error(), tt.errContain)
				}
				assert.Nil(t, prg)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, prg)
			}
		})
	}
}

func TestCompileBucketKey_Evaluation(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["type"], attributes["region"]])`)
	require.NoError(t, err)

	result, _, err := prg.Eval(map[string]any{
		"attributes": map[string]string{
			"type":   "carbon",
			"region": "eu-west",
		},
	})
	require.NoError(t, err)

	// bucket_key returns a hex-encoded SHA256 hash
	hash := result.Value().(string)
	assert.Len(t, hash, 64) // SHA256 = 32 bytes = 64 hex chars
}

func TestBucketKey_Deterministic(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["a"], attributes["b"]])`)
	require.NoError(t, err)

	attrs := map[string]string{"a": "foo", "b": "bar"}
	input := map[string]any{"attributes": attrs}

	result1, _, _ := prg.Eval(input)
	result2, _, _ := prg.Eval(input)

	assert.Equal(t, result1.Value(), result2.Value(), "same input should produce same hash")
}

func TestBucketKey_PreventDelimiterInjection(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["a"], attributes["b"]])`)
	require.NoError(t, err)

	// These two inputs would produce the same result with naive delimiter concatenation:
	// "ab" + "cd" == "a" + "bcd" (both would be "ab:cd" with ":" delimiter)
	// But with length-prefixed encoding, they should be different.
	input1 := map[string]any{"attributes": map[string]string{"a": "ab", "b": "cd"}}
	input2 := map[string]any{"attributes": map[string]string{"a": "a", "b": "bcd"}}

	result1, _, _ := prg.Eval(input1)
	result2, _, _ := prg.Eval(input2)

	assert.NotEqual(t, result1.Value(), result2.Value(),
		"different key arrangements should produce different hashes")
}

func TestExpressionTooLong(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Create an expression that exceeds MaxExpressionLength
	longExpr := strings.Repeat("true || ", MaxExpressionLength/8+1) + "true"
	require.Greater(t, len(longExpr), MaxExpressionLength)

	_, err = c.CompileValidation(longExpr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrExpressionTooLong)
}

func TestExpressionTooDeep(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Create an expression that exceeds MaxExpressionDepth
	// Each "((" adds 2 levels of nesting
	deepExpr := strings.Repeat("(", MaxExpressionDepth+2) + "true" + strings.Repeat(")", MaxExpressionDepth+2)

	_, err = c.CompileValidation(deepExpr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrExpressionTooDeep)
}

func TestMeasureExpressionDepth(t *testing.T) {
	tests := []struct {
		expr  string
		depth int
	}{
		{"true", 0},
		{"(true)", 1},
		{"((true))", 2},
		{"a(b(c(d)))", 3},
		{"[1, [2, [3]]]", 3},
		{`{"a": {"b": {"c": 1}}}`, 3},
		{"((((()))))", 5},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			assert.Equal(t, tt.depth, measureExpressionDepth(tt.expr))
		})
	}
}

func TestSafeParseLib_ParseISODate(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      string
		wantErr    bool
	}{
		{
			name:       "valid RFC3339",
			expression: `parse_iso_date("2024-01-15T10:30:00Z") != null`,
			wantErr:    false,
		},
		{
			name:       "valid with timezone",
			expression: `parse_iso_date("2024-01-15T10:30:00+01:00") != null`,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			now := time.Now()
			_, _, err = prg.Eval(map[string]any{
				"attributes": map[string]string{},
				"amount":     "0",
				"valid_from": now,
				"valid_to":   now,
				"source":     "",
			})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSafeParseLib_ParseInt(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		want       int64
		wantErr    bool
	}{
		{
			name:       "positive integer",
			expression: `parse_int("42")`,
			want:       42,
		},
		{
			name:       "negative integer",
			expression: `parse_int("-123")`,
			want:       -123,
		},
		{
			name:       "zero",
			expression: `parse_int("0")`,
			want:       0,
		},
	}

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "0",
		"valid_from": now,
		"valid_to":   now,
		"source":     "",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			result, _, err := prg.Eval(input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, result.Value())
			}
		})
	}
}

func TestSafeParseLib_ParseDecimal(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		want       float64
	}{
		{
			name:       "positive decimal",
			expression: `parse_decimal("3.14")`,
			want:       3.14,
		},
		{
			name:       "negative decimal",
			expression: `parse_decimal("-2.5")`,
			want:       -2.5,
		},
		{
			name:       "integer as decimal",
			expression: `parse_decimal("100")`,
			want:       100.0,
		},
	}

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "0",
		"valid_from": now,
		"valid_to":   now,
		"source":     "",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			result, _, err := prg.Eval(input)
			require.NoError(t, err)
			assert.InDelta(t, tt.want, result.Value(), 0.0001)
		})
	}
}

func TestSafeParseLib_ParseBool(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		want       bool
	}{
		{"true lowercase", `parse_bool("true")`, true},
		{"TRUE uppercase", `parse_bool("TRUE")`, true},
		{"1", `parse_bool("1")`, true},
		{"yes", `parse_bool("yes")`, true},
		{"on", `parse_bool("on")`, true},
		{"false lowercase", `parse_bool("false")`, false},
		{"FALSE uppercase", `parse_bool("FALSE")`, false},
		{"0", `parse_bool("0")`, false},
		{"no", `parse_bool("no")`, false},
		{"off", `parse_bool("off")`, false},
	}

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "0",
		"valid_from": now,
		"valid_to":   now,
		"source":     "",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			result, _, err := prg.Eval(input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.Value())
		})
	}
}

func TestSafeParseLib_InvalidInputs(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "0",
		"valid_from": now,
		"valid_to":   now,
		"source":     "",
	}

	tests := []struct {
		name       string
		expression string
	}{
		{"invalid date", `parse_iso_date("not-a-date")`},
		{"invalid int", `parse_int("not-a-number")`},
		{"invalid decimal", `parse_decimal("not-a-number")`},
		{"invalid bool", `parse_bool("maybe")`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := c.CompileValidation(tt.expression)
			require.NoError(t, err)

			_, _, err = prg.Eval(input)
			require.Error(t, err, "invalid input should produce evaluation error")
		})
	}
}

func BenchmarkCompileValidation(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	expression := `attributes["region"] == "us-east-1" && parse_decimal(amount) > 0.0`

	b.ResetTimer()
	for b.Loop() {
		_, err := c.CompileValidation(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluation(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileValidation(`attributes["region"] == "us-east-1" && parse_decimal(amount) > 0.0`)
	require.NoError(b, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{"region": "us-east-1"},
		"amount":     "100.50",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBucketKeyGeneration(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`)
	require.NoError(b, err)

	input := map[string]any{
		"attributes": map[string]string{
			"type":    "carbon",
			"region":  "eu-west",
			"vintage": "2024",
		},
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}
