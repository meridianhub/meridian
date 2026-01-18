package service

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
)

func TestNewCelValidator(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)
	require.NotNil(t, v)
	assert.NotNil(t, v.validationEnv)
	assert.NotNil(t, v.resolutionKeyEnv)
	assert.NotNil(t, v.errorMessageEnv)
	assert.NotNil(t, v.validationCache)
	assert.NotNil(t, v.resolutionKeyCache)
	assert.NotNil(t, v.errorMessageCache)
}

// =============================================================================
// Compilation Tests
// =============================================================================

func TestCompileValidation(t *testing.T) {
	v, err := NewCelValidator()
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
			name:       "decimal value check",
			expression: `decimal(value) > 0.0`,
			wantErr:    false,
		},
		{
			name:       "observation context access",
			expression: `observation_context["base_code"] == "USD"`,
			wantErr:    false,
		},
		{
			name:       "has check on observation context",
			expression: `has(observation_context.base_code)`,
			wantErr:    false,
		},
		{
			name:       "quality level comparison",
			expression: `quality >= 2`,
			wantErr:    false,
		},
		{
			name:       "source_id presence",
			expression: `source_id != ""`,
			wantErr:    false,
		},
		{
			name:       "timestamp comparison",
			expression: `valid_from < valid_to`,
			wantErr:    false,
		},
		{
			name:       "complex validation",
			expression: `decimal(value) > 0.0 && decimal(value) < 10000.0 && source_id != ""`,
			wantErr:    false,
		},
		{
			name:       "undefined variable",
			expression: `undefined_variable == "test"`,
			wantErr:    true,
			errContain: "undeclared reference",
		},
		{
			name:       "syntax error",
			expression: `observation_context["region" ==`,
			wantErr:    true,
			errContain: "CEL compilation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileValidation(tt.expression)
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

func TestCompileResolutionKey(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantErr    bool
		errContain string
	}{
		{
			name:       "simple concatenation",
			expression: `observation_context["base_code"] + "/" + observation_context["quote_code"]`,
			wantErr:    false,
		},
		{
			name:       "with has check",
			expression: `has(observation_context.base_code) ? observation_context.base_code : "UNKNOWN"`,
			wantErr:    false,
		},
		{
			name:       "FX rate key generation",
			expression: `has(observation_context.base_code) && has(observation_context.quote_code) ? observation_context.base_code + "/" + observation_context.quote_code : "INVALID"`,
			wantErr:    false,
		},
		{
			name:       "static key",
			expression: `"SPOT"`,
			wantErr:    false,
		},
		{
			name:       "invalid - uses value variable",
			expression: `value`, // value not in resolution key env
			wantErr:    true,
			errContain: "undeclared reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileResolutionKey(tt.expression)
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

func TestCompileErrorMessage(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		wantErr    bool
		errContain string
	}{
		{
			name:       "simple message",
			expression: `"Invalid value"`,
			wantErr:    false,
		},
		{
			name:       "with value",
			expression: `"Invalid price: " + value`,
			wantErr:    false,
		},
		{
			name:       "with dataset code",
			expression: `"Validation failed for " + dataset_code + ": " + value`,
			wantErr:    false,
		},
		{
			name:       "with context",
			expression: `"Invalid rate for " + observation_context["base_code"] + "/" + observation_context["quote_code"]`,
			wantErr:    false,
		},
		{
			name:       "invalid - uses quality",
			expression: `string(quality)`, // quality not in error message env
			wantErr:    true,
			errContain: "undeclared reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileErrorMessage(tt.expression)
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

// =============================================================================
// Caching Tests
// =============================================================================

func TestCompilationCaching(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	expression := `decimal(value) > 0.0`

	// First compilation
	prg1, err := v.CompileValidation(expression)
	require.NoError(t, err)

	// Second compilation should return cached program
	prg2, err := v.CompileValidation(expression)
	require.NoError(t, err)

	// Both should be the same program instance
	assert.Same(t, prg1, prg2, "cached program should be returned on second compilation")
}

func TestConcurrentCacheAccess(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	expression := `decimal(value) > 0.0`
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			prg, err := v.CompileValidation(expression)
			assert.NoError(t, err)
			assert.NotNil(t, prg)
		}()
	}

	wg.Wait()

	// Verify cache has only one entry
	v.validationMu.RLock()
	assert.Len(t, v.validationCache, 1)
	v.validationMu.RUnlock()
}

// =============================================================================
// Validation Expression Evaluation Tests
// =============================================================================

func TestEvaluateValidation_PositiveCases(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      ValidationInput
	}{
		{
			name:       "positive decimal value",
			expression: `decimal(value) > 0.0`,
			input: ValidationInput{
				Value:              "123.45",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "value within range",
			expression: `decimal(value) > 0.0 && decimal(value) < 10000.0`,
			input: ValidationInput{
				Value:              "1.2345",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "context attribute present",
			expression: `has(observation_context.base_code) && observation_context.base_code == "USD"`,
			input: ValidationInput{
				Value:              "1.05",
				ObservationContext: map[string]string{"base_code": "USD", "quote_code": "EUR"},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            3,
			},
		},
		{
			name:       "quality level check",
			expression: `quality >= 2`,
			input: ValidationInput{
				Value:              "100",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            3,
			},
		},
		{
			name:       "timestamp ordering",
			expression: `valid_from < valid_to`,
			input: ValidationInput{
				Value:              "100",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileValidation(tt.expression)
			require.NoError(t, err)

			result, err := v.EvaluateValidation(prg, tt.input)
			require.NoError(t, err)
			assert.True(t, result, "validation should pass")
		})
	}
}

func TestEvaluateValidation_NegativeCases(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      ValidationInput
	}{
		{
			name:       "negative value fails positive check",
			expression: `decimal(value) > 0.0`,
			input: ValidationInput{
				Value:              "-123.45",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "zero fails positive check",
			expression: `decimal(value) > 0.0`,
			input: ValidationInput{
				Value:              "0",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "value exceeds maximum",
			expression: `decimal(value) < 10000.0`,
			input: ValidationInput{
				Value:              "15000.00",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "missing required context attribute",
			expression: `has(observation_context.base_code) && observation_context.base_code == "USD"`,
			input: ValidationInput{
				Value:              "1.05",
				ObservationContext: map[string]string{}, // no base_code
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            2,
			},
		},
		{
			name:       "low quality level",
			expression: `quality >= 2`,
			input: ValidationInput{
				Value:              "100",
				ObservationContext: map[string]string{},
				ObservedAt:         time.Now(),
				ValidFrom:          time.Now(),
				ValidTo:            time.Now().Add(time.Hour),
				SourceID:           "test-source",
				Quality:            1, // Estimate
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileValidation(tt.expression)
			require.NoError(t, err)

			result, err := v.EvaluateValidation(prg, tt.input)
			require.NoError(t, err)
			assert.False(t, result, "validation should fail")
		})
	}
}

// =============================================================================
// Resolution Key Evaluation Tests
// =============================================================================

func TestEvaluateResolutionKey(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      ResolutionKeyInput
		want       string
	}{
		{
			name:       "FX rate key USD/EUR",
			expression: `observation_context["base_code"] + "/" + observation_context["quote_code"]`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{"base_code": "USD", "quote_code": "EUR"},
			},
			want: "USD/EUR",
		},
		{
			name:       "FX rate key EUR/GBP",
			expression: `observation_context["base_code"] + "/" + observation_context["quote_code"]`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{"base_code": "EUR", "quote_code": "GBP"},
			},
			want: "EUR/GBP",
		},
		{
			name:       "static spot key",
			expression: `"SPOT"`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{},
			},
			want: "SPOT",
		},
		{
			name:       "with has guard - present",
			expression: `has(observation_context.base_code) && has(observation_context.quote_code) ? observation_context.base_code + "/" + observation_context.quote_code : "UNKNOWN"`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{"base_code": "USD", "quote_code": "JPY"},
			},
			want: "USD/JPY",
		},
		{
			name:       "with has guard - missing",
			expression: `has(observation_context.base_code) && has(observation_context.quote_code) ? observation_context.base_code + "/" + observation_context.quote_code : "UNKNOWN"`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{"base_code": "USD"}, // missing quote_code
			},
			want: "UNKNOWN",
		},
		{
			name:       "tenor-based key",
			expression: `observation_context["tenor"] + ":" + observation_context["settlement_type"]`,
			input: ResolutionKeyInput{
				ObservationContext: map[string]string{"tenor": "1M", "settlement_type": "T+2"},
			},
			want: "1M:T+2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileResolutionKey(tt.expression)
			require.NoError(t, err)

			result, err := v.EvaluateResolutionKey(prg, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

// =============================================================================
// Error Message Evaluation Tests
// =============================================================================

func TestEvaluateErrorMessage(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      ErrorMessageInput
		want       string
	}{
		{
			name:       "simple message",
			expression: `"Invalid value"`,
			input: ErrorMessageInput{
				Value:              "123",
				ObservationContext: map[string]string{},
				DatasetCode:        "FX_RATE",
			},
			want: "Invalid value",
		},
		{
			name:       "with value",
			expression: `"Invalid price: " + value`,
			input: ErrorMessageInput{
				Value:              "-50.00",
				ObservationContext: map[string]string{},
				DatasetCode:        "FX_RATE",
			},
			want: "Invalid price: -50.00",
		},
		{
			name:       "with dataset code",
			expression: `"Validation failed for " + dataset_code + ": value " + value + " is invalid"`,
			input: ErrorMessageInput{
				Value:              "99999",
				ObservationContext: map[string]string{},
				DatasetCode:        "GOLD_PRICE",
			},
			want: "Validation failed for GOLD_PRICE: value 99999 is invalid",
		},
		{
			name:       "with context",
			expression: `"Invalid rate for " + observation_context["base_code"] + "/" + observation_context["quote_code"] + ": " + value`,
			input: ErrorMessageInput{
				Value:              "-1.05",
				ObservationContext: map[string]string{"base_code": "USD", "quote_code": "EUR"},
				DatasetCode:        "FX_RATE",
			},
			want: "Invalid rate for USD/EUR: -1.05",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileErrorMessage(tt.expression)
			require.NoError(t, err)

			result, err := v.EvaluateErrorMessage(prg, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

// =============================================================================
// Decimal Function Tests
// =============================================================================

func TestDecimalFunction(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		input      ValidationInput
		want       bool
	}{
		{
			name:       "positive integer",
			expression: `decimal("123") == 123.0`,
			input:      ValidationInput{Value: "0"},
			want:       true,
		},
		{
			name:       "positive decimal",
			expression: `decimal("123.45") > 123.0`,
			input:      ValidationInput{Value: "0"},
			want:       true,
		},
		{
			name:       "negative decimal",
			expression: `decimal("-50.5") < 0.0`,
			input:      ValidationInput{Value: "0"},
			want:       true,
		},
		{
			name:       "very small decimal",
			expression: `decimal("0.0001") > 0.0`,
			input:      ValidationInput{Value: "0"},
			want:       true,
		},
		{
			name:       "large decimal",
			expression: `decimal("999999.99") > 100000.0`,
			input:      ValidationInput{Value: "0"},
			want:       true,
		},
		{
			name:       "using value variable",
			expression: `decimal(value) > 0.0`,
			input:      ValidationInput{Value: "100.50"},
			want:       true,
		},
	}

	now := time.Now()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileValidation(tt.expression)
			require.NoError(t, err)

			tt.input.ObservationContext = map[string]string{}
			tt.input.ObservedAt = now
			tt.input.ValidFrom = now
			tt.input.ValidTo = now.Add(time.Hour)
			tt.input.SourceID = "test"
			tt.input.Quality = 2

			result, err := v.EvaluateValidation(prg, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestDecimalFunction_InvalidInputs(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
	}{
		{
			name:       "invalid string",
			expression: `decimal("not-a-number") > 0.0`,
		},
		{
			name:       "empty string",
			expression: `decimal("") > 0.0`,
		},
		{
			name:       "special characters",
			expression: `decimal("$100.00") > 0.0`,
		},
	}

	now := time.Now()
	input := ValidationInput{
		Value:              "0",
		ObservationContext: map[string]string{},
		ObservedAt:         now,
		ValidFrom:          now,
		ValidTo:            now.Add(time.Hour),
		SourceID:           "test",
		Quality:            2,
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prg, err := v.CompileValidation(tt.expression)
			require.NoError(t, err)

			_, err = v.EvaluateValidation(prg, input)
			require.Error(t, err, "invalid decimal input should produce evaluation error")
		})
	}
}

// =============================================================================
// Security Limit Tests
// =============================================================================

func TestExpressionTooLong(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	// Create an expression that exceeds MaxExpressionLength
	longExpr := strings.Repeat("true || ", MaxExpressionLength/8+1) + "true"
	require.Greater(t, len(longExpr), MaxExpressionLength)

	_, err = v.CompileValidation(longExpr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrExpressionTooLong)
}

func TestExpressionTooDeep(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	// Create an expression that exceeds MaxExpressionDepth
	deepExpr := strings.Repeat("(", MaxExpressionDepth+2) + "true" + strings.Repeat(")", MaxExpressionDepth+2)

	_, err = v.CompileValidation(deepExpr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrExpressionTooDeep)
}

func TestValidExpressionWithinLimits(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	// A reasonably complex expression that should still be within limits
	expr := `decimal(value) > 0.0 && decimal(value) < 10000.0 && has(observation_context.base_code) && observation_context.base_code != "" && quality >= 2 && source_id != ""`

	prg, err := v.CompileValidation(expr)
	require.NoError(t, err)
	assert.NotNil(t, prg)
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
		{`observation_context["key"]`, 1},
		{`has(observation_context.key)`, 1},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			assert.Equal(t, tt.depth, measureExpressionDepth(tt.expr))
		})
	}
}

// =============================================================================
// ToContextMap Tests
// =============================================================================

func TestToContextMap(t *testing.T) {
	tests := []struct {
		name    string
		entries []*quantityv1.AttributeEntry
		want    map[string]string
	}{
		{
			name:    "nil entries",
			entries: nil,
			want:    map[string]string{},
		},
		{
			name:    "empty entries",
			entries: []*quantityv1.AttributeEntry{},
			want:    map[string]string{},
		},
		{
			name: "single entry",
			entries: []*quantityv1.AttributeEntry{
				{Key: "base_code", Value: "USD"},
			},
			want: map[string]string{"base_code": "USD"},
		},
		{
			name: "multiple entries",
			entries: []*quantityv1.AttributeEntry{
				{Key: "base_code", Value: "USD"},
				{Key: "quote_code", Value: "EUR"},
				{Key: "tenor", Value: "1M"},
			},
			want: map[string]string{
				"base_code":  "USD",
				"quote_code": "EUR",
				"tenor":      "1M",
			},
		},
		{
			name: "with nil entry in slice",
			entries: []*quantityv1.AttributeEntry{
				{Key: "base_code", Value: "USD"},
				nil,
				{Key: "quote_code", Value: "EUR"},
			},
			want: map[string]string{
				"base_code":  "USD",
				"quote_code": "EUR",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToContextMap(tt.entries)
			assert.Equal(t, tt.want, result)
		})
	}
}

// =============================================================================
// Integration Test: FX Rate Validation (Real-world Example)
// =============================================================================

func TestIntegration_FXRateValidation(t *testing.T) {
	v, err := NewCelValidator()
	require.NoError(t, err)

	// Compile the expressions from a typical FX rate dataset definition
	validationExpr := `decimal(value) > 0.0 && decimal(value) < 1000.0 && has(observation_context.base_code) && has(observation_context.quote_code)`
	resolutionKeyExpr := `observation_context["base_code"] + "/" + observation_context["quote_code"]`
	errorMessageExpr := `"Invalid FX rate for " + observation_context["base_code"] + "/" + observation_context["quote_code"] + ": " + value + " must be positive and < 1000"`

	validationPrg, err := v.CompileValidation(validationExpr)
	require.NoError(t, err)

	resolutionKeyPrg, err := v.CompileResolutionKey(resolutionKeyExpr)
	require.NoError(t, err)

	errorMessagePrg, err := v.CompileErrorMessage(errorMessageExpr)
	require.NoError(t, err)

	// Test valid observation
	t.Run("valid FX rate", func(t *testing.T) {
		now := time.Now()
		obsContext := map[string]string{"base_code": "EUR", "quote_code": "USD"}

		// Validate
		validInput := ValidationInput{
			Value:              "1.0856",
			ObservationContext: obsContext,
			ObservedAt:         now,
			ValidFrom:          now,
			ValidTo:            now.Add(24 * time.Hour),
			SourceID:           "ECB",
			Quality:            3,
		}
		isValid, err := v.EvaluateValidation(validationPrg, validInput)
		require.NoError(t, err)
		assert.True(t, isValid)

		// Generate resolution key
		keyInput := ResolutionKeyInput{ObservationContext: obsContext}
		key, err := v.EvaluateResolutionKey(resolutionKeyPrg, keyInput)
		require.NoError(t, err)
		assert.Equal(t, "EUR/USD", key)
	})

	// Test invalid observation (negative rate)
	t.Run("invalid FX rate - negative", func(t *testing.T) {
		now := time.Now()
		obsContext := map[string]string{"base_code": "EUR", "quote_code": "USD"}

		validInput := ValidationInput{
			Value:              "-1.0856",
			ObservationContext: obsContext,
			ObservedAt:         now,
			ValidFrom:          now,
			ValidTo:            now.Add(24 * time.Hour),
			SourceID:           "ECB",
			Quality:            3,
		}
		isValid, err := v.EvaluateValidation(validationPrg, validInput)
		require.NoError(t, err)
		assert.False(t, isValid)

		// Generate error message
		errInput := ErrorMessageInput{
			Value:              "-1.0856",
			ObservationContext: obsContext,
			DatasetCode:        "FX_RATE",
		}
		errMsg, err := v.EvaluateErrorMessage(errorMessagePrg, errInput)
		require.NoError(t, err)
		assert.Equal(t, "Invalid FX rate for EUR/USD: -1.0856 must be positive and < 1000", errMsg)
	})

	// Test invalid observation (exceeds maximum)
	t.Run("invalid FX rate - too large", func(t *testing.T) {
		now := time.Now()
		obsContext := map[string]string{"base_code": "USD", "quote_code": "JPY"}

		validInput := ValidationInput{
			Value:              "1500.0",
			ObservationContext: obsContext,
			ObservedAt:         now,
			ValidFrom:          now,
			ValidTo:            now.Add(24 * time.Hour),
			SourceID:           "ECB",
			Quality:            2,
		}
		isValid, err := v.EvaluateValidation(validationPrg, validInput)
		require.NoError(t, err)
		assert.False(t, isValid)
	})

	// Test invalid observation (missing context)
	t.Run("invalid FX rate - missing context", func(t *testing.T) {
		now := time.Now()
		obsContext := map[string]string{"base_code": "EUR"} // missing quote_code

		validInput := ValidationInput{
			Value:              "1.0856",
			ObservationContext: obsContext,
			ObservedAt:         now,
			ValidFrom:          now,
			ValidTo:            now.Add(24 * time.Hour),
			SourceID:           "ECB",
			Quality:            3,
		}
		isValid, err := v.EvaluateValidation(validationPrg, validInput)
		require.NoError(t, err)
		assert.False(t, isValid)
	})
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkCompileValidation(b *testing.B) {
	v, err := NewCelValidator()
	require.NoError(b, err)

	// Use a new expression each time to avoid cache hits
	expressions := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		expressions[i] = `decimal(value) > 0.0 && quality >= 2`
	}

	// Reset cache before benchmark
	v.validationCache = make(map[string]cel.Program)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := v.CompileValidation(expressions[i])
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileValidation_Cached(b *testing.B) {
	v, err := NewCelValidator()
	require.NoError(b, err)

	expression := `decimal(value) > 0.0 && quality >= 2`

	// Pre-populate cache
	_, err = v.CompileValidation(expression)
	require.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := v.CompileValidation(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluateValidation(b *testing.B) {
	v, err := NewCelValidator()
	require.NoError(b, err)

	prg, err := v.CompileValidation(`decimal(value) > 0.0 && decimal(value) < 10000.0 && quality >= 2`)
	require.NoError(b, err)

	now := time.Now()
	input := ValidationInput{
		Value:              "123.45",
		ObservationContext: map[string]string{"base_code": "USD", "quote_code": "EUR"},
		ObservedAt:         now,
		ValidFrom:          now,
		ValidTo:            now.Add(time.Hour),
		SourceID:           "test-source",
		Quality:            3,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := v.EvaluateValidation(prg, input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluateResolutionKey(b *testing.B) {
	v, err := NewCelValidator()
	require.NoError(b, err)

	prg, err := v.CompileResolutionKey(`observation_context["base_code"] + "/" + observation_context["quote_code"]`)
	require.NoError(b, err)

	input := ResolutionKeyInput{
		ObservationContext: map[string]string{"base_code": "USD", "quote_code": "EUR"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := v.EvaluateResolutionKey(prg, input)
		if err != nil {
			b.Fatal(err)
		}
	}
}
