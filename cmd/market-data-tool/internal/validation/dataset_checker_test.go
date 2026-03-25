package validation

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDatasetChecker(t *testing.T) {
	t.Run("creates checker with configured dataset code", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "USD_EUR_FX")
		require.NotNil(t, checker)
		assert.False(t, checker.IsChecked())
		assert.False(t, checker.Exists())
		assert.False(t, checker.IsActive())
	})
}

func TestDatasetChecker_CodeMismatch(t *testing.T) {
	t.Run("returns ErrDatasetCodeMismatch when codes differ", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "USD_EUR_FX")

		err := checker.Check(nil, "OTHER_DATASET") //nolint:staticcheck // nil context is intentional for unit testing the mismatch path
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrDatasetCodeMismatch))
	})

	t.Run("includes expected and actual codes in error message", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "USD_EUR_FX")

		err := checker.Check(nil, "WRONG_CODE") //nolint:staticcheck // nil context is intentional for unit testing the mismatch path
		require.Error(t, err)
		assert.Contains(t, err.Error(), "USD_EUR_FX")
		assert.Contains(t, err.Error(), "WRONG_CODE")
	})
}

func TestDatasetChecker_Reset(t *testing.T) {
	t.Run("Reset clears cached state", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "USD_EUR_FX")

		// Manually set state (simulating a completed check)
		checker.mu.Lock()
		checker.checked = true
		checker.exists = true
		checker.isActive = true
		checker.mu.Unlock()

		assert.True(t, checker.IsChecked())
		assert.True(t, checker.Exists())
		assert.True(t, checker.IsActive())

		checker.Reset()

		assert.False(t, checker.IsChecked())
		assert.False(t, checker.Exists())
		assert.False(t, checker.IsActive())
	})

	t.Run("Reset clears cached error", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "USD_EUR_FX")

		checker.mu.Lock()
		checker.checked = true
		checker.err = ErrDatasetNotFound
		checker.mu.Unlock()

		checker.Reset()

		assert.False(t, checker.IsChecked())
		// Verify the error was also cleared
		checker.mu.RLock()
		assert.Nil(t, checker.err)
		checker.mu.RUnlock()
	})
}

func TestDatasetChecker_Getters(t *testing.T) {
	t.Run("IsChecked returns false initially", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "DS")
		assert.False(t, checker.IsChecked())
	})

	t.Run("Exists returns false initially", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "DS")
		assert.False(t, checker.Exists())
	})

	t.Run("IsActive returns false initially", func(t *testing.T) {
		checker := NewDatasetChecker(nil, "DS")
		assert.False(t, checker.IsActive())
	})
}

func TestDatasetCheckerErrors(t *testing.T) {
	t.Run("ErrDatasetNotFound is defined", func(t *testing.T) {
		assert.NotNil(t, ErrDatasetNotFound)
		assert.Contains(t, ErrDatasetNotFound.Error(), "not found")
	})

	t.Run("ErrDatasetNotActive is defined", func(t *testing.T) {
		assert.NotNil(t, ErrDatasetNotActive)
		assert.Contains(t, ErrDatasetNotActive.Error(), "ACTIVE")
	})

	t.Run("ErrDatasetCodeMismatch is defined", func(t *testing.T) {
		assert.NotNil(t, ErrDatasetCodeMismatch)
	})
}
