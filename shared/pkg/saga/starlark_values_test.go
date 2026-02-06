package saga

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
)

// Tests for sagaDefinitionValue Starlark interface methods.
func TestSagaDefinitionValue(t *testing.T) {
	v := &sagaDefinitionValue{name: "test-saga"}

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, `saga("test-saga")`, v.String())
	})

	t.Run("Type", func(t *testing.T) {
		assert.Equal(t, "SagaDefinition", v.Type())
	})

	t.Run("Freeze", func(t *testing.T) {
		assert.False(t, v.frozen)
		v.Freeze()
		assert.True(t, v.frozen)
	})

	t.Run("Truth", func(t *testing.T) {
		assert.Equal(t, starlark.True, v.Truth())
	})

	t.Run("Hash", func(t *testing.T) {
		h, err := v.Hash()
		require.NoError(t, err)
		// Should match the hash of the name string
		expectedHash, _ := starlark.String("test-saga").Hash()
		assert.Equal(t, expectedHash, h)
	})
}

// Tests for stepDefinitionValue Starlark interface methods.
func TestStepDefinitionValue(t *testing.T) {
	v := &stepDefinitionValue{name: "validate_balance"}

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, `step("validate_balance")`, v.String())
	})

	t.Run("Type", func(t *testing.T) {
		assert.Equal(t, "StepDefinition", v.Type())
	})

	t.Run("Freeze", func(t *testing.T) {
		assert.False(t, v.frozen)
		v.Freeze()
		assert.True(t, v.frozen)
	})

	t.Run("Truth", func(t *testing.T) {
		assert.Equal(t, starlark.True, v.Truth())
	})

	t.Run("Hash", func(t *testing.T) {
		h, err := v.Hash()
		require.NoError(t, err)
		expectedHash, _ := starlark.String("validate_balance").Hash()
		assert.Equal(t, expectedHash, h)
	})
}

// Tests for postingValue Starlark interface methods.
func TestPostingValue(t *testing.T) {
	v := &postingValue{debit: "ACC001", credit: "ACC002", amount: "100.00"}

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, `posting("ACC001", "ACC002", "100.00")`, v.String())
	})

	t.Run("Type", func(t *testing.T) {
		assert.Equal(t, "Posting", v.Type())
	})

	t.Run("Freeze", func(t *testing.T) {
		assert.False(t, v.frozen)
		v.Freeze()
		assert.True(t, v.frozen)
	})

	t.Run("Truth", func(t *testing.T) {
		assert.Equal(t, starlark.True, v.Truth())
	})

	t.Run("Hash returns unhashable error", func(t *testing.T) {
		_, err := v.Hash()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnhashable)
	})
}

// Tests for sagaResultValue Starlark interface methods.
func TestSagaResultValue(t *testing.T) {
	execID := uuid.New()
	output := starlark.NewDict(1)
	_ = output.SetKey(starlark.String("key"), starlark.String("value"))

	v := &sagaResultValue{
		executionID:    execID,
		status:         ResultStatusCompleted,
		output:         output,
		stepsCompleted: 3,
	}

	t.Run("String", func(t *testing.T) {
		expected := fmt.Sprintf(`Result(execution_id=%q, status=%q)`, execID, ResultStatusCompleted)
		assert.Equal(t, expected, v.String())
	})

	t.Run("Type", func(t *testing.T) {
		assert.Equal(t, "Result", v.Type())
	})

	t.Run("Freeze", func(t *testing.T) {
		assert.False(t, v.frozen)
		v.Freeze()
		assert.True(t, v.frozen)
	})

	t.Run("Truth is true for COMPLETED", func(t *testing.T) {
		assert.Equal(t, starlark.True, v.Truth())
	})

	t.Run("Truth is false for FAILED", func(t *testing.T) {
		failed := &sagaResultValue{status: ResultStatusFailed}
		assert.Equal(t, starlark.False, failed.Truth())
	})

	t.Run("Hash returns unhashable error", func(t *testing.T) {
		_, err := v.Hash()
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnhashable)
	})

	t.Run("Attr returns execution_id", func(t *testing.T) {
		val, err := v.Attr("execution_id")
		require.NoError(t, err)
		assert.Equal(t, starlark.String(execID.String()), val)
	})

	t.Run("Attr returns status", func(t *testing.T) {
		val, err := v.Attr("status")
		require.NoError(t, err)
		assert.Equal(t, starlark.String(ResultStatusCompleted), val)
	})

	t.Run("Attr returns output", func(t *testing.T) {
		val, err := v.Attr("output")
		require.NoError(t, err)
		assert.Equal(t, output, val)
	})

	t.Run("Attr returns steps_completed", func(t *testing.T) {
		val, err := v.Attr("steps_completed")
		require.NoError(t, err)
		assert.Equal(t, starlark.MakeInt(3), val)
	})

	t.Run("Attr returns error for unknown attribute", func(t *testing.T) {
		_, err := v.Attr("nonexistent")
		require.Error(t, err)
	})

	t.Run("AttrNames returns all attribute names", func(t *testing.T) {
		names := v.AttrNames()
		assert.Equal(t, []string{"execution_id", "status", "output", "steps_completed"}, names)
	})
}
