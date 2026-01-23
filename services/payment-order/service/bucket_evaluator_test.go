package service

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBucketEvaluator_NewBucketEvaluator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)
	assert.NotNil(t, evaluator)
	assert.NotNil(t, evaluator.env)
	assert.NotNil(t, evaluator.cache)
}

func TestBucketEvaluator_Evaluate_EmptyExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), "", BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})

	require.NoError(t, err)
	assert.Equal(t, "", result, "empty expression should return empty bucket ID")
}

func TestBucketEvaluator_Evaluate_SimpleString(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), `"default-bucket"`, BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})

	require.NoError(t, err)
	assert.Equal(t, "default-bucket", result)
}

func TestBucketEvaluator_Evaluate_InstrumentCodeOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), `instrument_code`, BucketEvalContext{
		InstrumentCode: "RICE_V1",
		Attributes:     nil,
	})

	require.NoError(t, err)
	assert.Equal(t, "RICE_V1", result)
}

func TestBucketEvaluator_Evaluate_WithAttributes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(),
		`instrument_code + ":" + attributes.grade`,
		BucketEvalContext{
			InstrumentCode: "RICE_V1",
			Attributes:     map[string]string{"grade": "A"},
		})

	require.NoError(t, err)
	assert.Equal(t, "RICE_V1:A", result)
}

func TestBucketEvaluator_Evaluate_MultipleAttributes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	// Complex expression with multiple attributes
	result, err := evaluator.Evaluate(context.Background(),
		`instrument_code + ":" + attributes.grade + "-" + attributes.origin`,
		BucketEvalContext{
			InstrumentCode: "RICE_V1",
			Attributes: map[string]string{
				"grade":  "A",
				"origin": "TH",
			},
		})

	require.NoError(t, err)
	assert.Equal(t, "RICE_V1:A-TH", result)
}

func TestBucketEvaluator_Evaluate_Caching(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	expression := `instrument_code + ":cached"`

	// First evaluation - compiles and caches
	result1, err := evaluator.Evaluate(context.Background(), expression, BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})
	require.NoError(t, err)
	assert.Equal(t, "USD:cached", result1)

	// Verify cache has the expression
	assert.Len(t, evaluator.cache, 1)

	// Second evaluation - uses cache
	result2, err := evaluator.Evaluate(context.Background(), expression, BucketEvalContext{
		InstrumentCode: "EUR",
		Attributes:     nil,
	})
	require.NoError(t, err)
	assert.Equal(t, "EUR:cached", result2)

	// Cache should still have only one entry
	assert.Len(t, evaluator.cache, 1)
}

func TestBucketEvaluator_Evaluate_InvalidExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	_, err = evaluator.Evaluate(context.Background(), `undefined_variable`, BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CEL compilation error")
}

func TestBucketEvaluator_Evaluate_MissingAttribute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	// Accessing a key that doesn't exist in attributes map causes runtime error
	_, err = evaluator.Evaluate(context.Background(),
		`attributes.nonexistent`,
		BucketEvalContext{
			InstrumentCode: "USD",
			Attributes:     map[string]string{},
		})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "CEL evaluation failed")
}

func TestBucketEvaluator_Evaluate_ConditionalExpression(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	expression := `has(attributes.grade) ? instrument_code + ":" + attributes.grade : instrument_code`

	// With grade attribute
	result1, err := evaluator.Evaluate(context.Background(), expression, BucketEvalContext{
		InstrumentCode: "RICE_V1",
		Attributes:     map[string]string{"grade": "A"},
	})
	require.NoError(t, err)
	assert.Equal(t, "RICE_V1:A", result1)

	// Without grade attribute
	result2, err := evaluator.Evaluate(context.Background(), expression, BucketEvalContext{
		InstrumentCode: "RICE_V1",
		Attributes:     map[string]string{},
	})
	require.NoError(t, err)
	assert.Equal(t, "RICE_V1", result2)
}

func TestBucketEvaluator_Evaluate_IntegerResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), `42`, BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})

	require.NoError(t, err)
	assert.Equal(t, "42", result)
}

func TestBucketEvaluator_Evaluate_BooleanResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	evaluator, err := NewBucketEvaluator(logger)
	require.NoError(t, err)

	result, err := evaluator.Evaluate(context.Background(), `true`, BucketEvalContext{
		InstrumentCode: "USD",
		Attributes:     nil,
	})

	require.NoError(t, err)
	assert.Equal(t, "true", result)
}
