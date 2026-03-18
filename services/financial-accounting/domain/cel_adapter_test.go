package domain

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCELProgram implements the interface expected by NewCELProgramAdapter.
// It models a cel.Program that returns ref.Val on Eval.
type mockCELProgram struct {
	result interface{ Value() interface{} }
	detail interface{}
	err    error
}

func (m *mockCELProgram) Eval(_ interface{}) (interface{ Value() interface{} }, interface{}, error) {
	return m.result, m.detail, m.err
}

// mockRefVal implements the Value() interface{} method returned by CEL programs.
type mockRefVal struct {
	value interface{}
}

func (m *mockRefVal) Value() interface{} {
	return m.value
}

func TestNewCELProgramAdapter_WithValidProgram(t *testing.T) {
	program := &mockCELProgram{
		result: &mockRefVal{value: "test-key"},
	}

	adapter := NewCELProgramAdapter(program)
	require.NotNil(t, adapter)
}

func TestNewCELProgramAdapter_WithInvalidProgram(t *testing.T) {
	// Pass an object that doesn't implement the Eval interface
	adapter := NewCELProgramAdapter("not a program")
	assert.Nil(t, adapter)
}

func TestCELProgramAdapter_Eval_ReturnsStringValue(t *testing.T) {
	program := &mockCELProgram{
		result: &mockRefVal{value: "batch-2024:grade-1"},
	}
	adapter := NewCELProgramAdapter(program)
	require.NotNil(t, adapter)

	result, err := adapter.Eval(map[string]interface{}{"attributes": map[string]string{"batch": "2024"}})
	require.NoError(t, err)
	assert.Equal(t, "batch-2024:grade-1", result)
}

func TestCELProgramAdapter_Eval_ProgramError(t *testing.T) {
	program := &mockCELProgram{
		err: errors.New("cel evaluation failed"),
	}
	adapter := NewCELProgramAdapter(program)
	require.NotNil(t, adapter)

	_, err := adapter.Eval(map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cel evaluation failed")
}

func TestCELProgramAdapter_Eval_NilResult(t *testing.T) {
	program := &mockCELProgram{
		result: nil,
	}
	adapter := NewCELProgramAdapter(program)
	require.NotNil(t, adapter)

	result, err := adapter.Eval(map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestCELProgramAdapter_Eval_NilProgram(t *testing.T) {
	adapter := &CELProgramAdapter{program: nil}

	result, err := adapter.Eval(map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

// mockFungibilityProgram returns a ref.Val-like result (non-string directly) for testing
// the evaluateFungibilityKey switch default branch.
type mockValuerProgram struct {
	key   string
	err   error
	asVal bool // if true, returns Value()-based result; if false returns string directly
}

func (m *mockValuerProgram) Eval(_ interface{}) (interface{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.asVal {
		return &mockRefVal{value: m.key}, nil
	}
	return m.key, nil
}

func TestEvaluateFungibilityKey_ValuerInterfacePath(t *testing.T) {
	// When Eval returns a value implementing Value() interface{} (like cel.Program does)
	program := &mockValuerProgram{key: "grade:1", asVal: true}
	result, err := evaluateFungibilityKey(program, map[string]string{"grade": "1"})
	require.NoError(t, err)
	assert.Equal(t, "grade:1", result)
}

func TestEvaluateFungibilityKey_StringPath(t *testing.T) {
	// When Eval returns a string directly (like our mock implementations)
	program := &mockValuerProgram{key: "grade:1", asVal: false}
	result, err := evaluateFungibilityKey(program, map[string]string{"grade": "1"})
	require.NoError(t, err)
	assert.Equal(t, "grade:1", result)
}

func TestEvaluateFungibilityKey_EvalError(t *testing.T) {
	program := &mockValuerProgram{err: errors.New("eval failed")}
	_, err := evaluateFungibilityKey(program, map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval failed")
}

func TestValidateFungibility_EvalDebitError(t *testing.T) {
	// When evaluating debit attributes fails
	program := &mockValuerProgram{err: errors.New("debit eval error")}

	err := ValidateFungibility(program, map[string]string{"grade": "1"}, map[string]string{"grade": "2"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFungibilityKeyEvaluation)
	assert.Contains(t, err.Error(), "debit attributes")
}

func TestValidateFungibility_EvalCreditError(t *testing.T) {
	// First debit call succeeds, second (credit) call fails
	evalCount := 0
	errProgram := &errOnSecondEvalProgram{evalCount: &evalCount, errorMsg: "credit eval error"}
	err := ValidateFungibility(errProgram, map[string]string{"grade": "1"}, map[string]string{"grade": "2"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFungibilityKeyEvaluation)
	assert.Contains(t, err.Error(), "credit attributes")
}

// errOnSecondEvalProgram fails on the second Eval call.
type errOnSecondEvalProgram struct {
	evalCount *int
	errorMsg  string
}

func (e *errOnSecondEvalProgram) Eval(_ interface{}) (interface{}, error) {
	*e.evalCount++
	if *e.evalCount == 2 {
		return nil, errors.New(e.errorMsg)
	}
	return "some-key", nil
}

func TestEvaluateFungibilityKey_InvalidResultType(t *testing.T) {
	// When Eval returns a type that is neither string nor implements Value() interface{}
	program := &intResultProgram{}
	_, err := evaluateFungibilityKey(program, map[string]string{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFungibilityKeyResultType)
}

// intResultProgram returns an integer from Eval, which is an invalid result type.
type intResultProgram struct{}

func (i *intResultProgram) Eval(_ interface{}) (interface{}, error) {
	return 42, nil // int is neither string nor implements Value()
}
