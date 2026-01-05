package validator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettlementLockError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &SettlementLockError{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
			PositionCount:     5,
			SettlementIDs:     []string{"settle-1", "settle-2"},
		}

		msg := err.Error()
		assert.Contains(t, msg, "USD")
		assert.Contains(t, msg, "v1")
		assert.Contains(t, msg, "5 positions")
		assert.Contains(t, msg, "settle-1")
		assert.Contains(t, msg, "settle-2")
	})

	t.Run("Is matches SettlementLockError", func(t *testing.T) {
		err := &SettlementLockError{InstrumentCode: "USD", InstrumentVersion: 1}
		target := &SettlementLockError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})

	t.Run("Is does not match other errors", func(t *testing.T) {
		err := &SettlementLockError{InstrumentCode: "USD", InstrumentVersion: 1}

		assert.False(t, err.Is(errServiceUnavailable))
		assert.False(t, err.Is(&InstrumentNotFoundError{}))
	})
}

func TestInstrumentNotFoundError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &InstrumentNotFoundError{
			InstrumentCode:    "KWH",
			InstrumentVersion: 2,
		}

		msg := err.Error()
		assert.Contains(t, msg, "KWH")
		assert.Contains(t, msg, "v2")
		assert.Contains(t, msg, "does not exist")
	})

	t.Run("Is matches InstrumentNotFoundError", func(t *testing.T) {
		err := &InstrumentNotFoundError{InstrumentCode: "KWH", InstrumentVersion: 2}
		target := &InstrumentNotFoundError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestInstrumentAlreadyDeprecatedError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &InstrumentAlreadyDeprecatedError{
			InstrumentCode:    "GPU_HOUR",
			InstrumentVersion: 3,
		}

		msg := err.Error()
		assert.Contains(t, msg, "GPU_HOUR")
		assert.Contains(t, msg, "v3")
		assert.Contains(t, msg, "already")
		assert.Contains(t, msg, "DEPRECATED")
	})

	t.Run("Is matches InstrumentAlreadyDeprecatedError", func(t *testing.T) {
		err := &InstrumentAlreadyDeprecatedError{InstrumentCode: "GPU_HOUR", InstrumentVersion: 3}
		target := &InstrumentAlreadyDeprecatedError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestInstrumentNotActiveError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &InstrumentNotActiveError{
			InstrumentCode:    "CARBON",
			InstrumentVersion: 1,
			CurrentStatus:     "DRAFT",
		}

		msg := err.Error()
		assert.Contains(t, msg, "CARBON")
		assert.Contains(t, msg, "v1")
		assert.Contains(t, msg, "DRAFT")
		assert.Contains(t, msg, "only ACTIVE instruments can be deprecated")
	})

	t.Run("Is matches InstrumentNotActiveError", func(t *testing.T) {
		err := &InstrumentNotActiveError{
			InstrumentCode:    "CARBON",
			InstrumentVersion: 1,
			CurrentStatus:     "DRAFT",
		}
		target := &InstrumentNotActiveError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestRawMeasurementsUnavailableError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &RawMeasurementsUnavailableError{
			InstrumentCode:    "MWH",
			InstrumentVersion: 1,
			Reason:            "measurements archived beyond retention period",
		}

		msg := err.Error()
		assert.Contains(t, msg, "MWH")
		assert.Contains(t, msg, "v1")
		assert.Contains(t, msg, "measurements archived")
	})

	t.Run("Is matches RawMeasurementsUnavailableError", func(t *testing.T) {
		err := &RawMeasurementsUnavailableError{
			InstrumentCode:    "MWH",
			InstrumentVersion: 1,
			Reason:            "test",
		}
		target := &RawMeasurementsUnavailableError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestInstrumentInUseError(t *testing.T) {
	t.Run("Error returns formatted message", func(t *testing.T) {
		err := &InstrumentInUseError{
			InstrumentCode:    "EUR",
			InstrumentVersion: 1,
			ActiveTradeCount:  15,
		}

		msg := err.Error()
		assert.Contains(t, msg, "EUR")
		assert.Contains(t, msg, "v1")
		assert.Contains(t, msg, "15 active trades")
	})

	t.Run("Is matches InstrumentInUseError", func(t *testing.T) {
		err := &InstrumentInUseError{
			InstrumentCode:    "EUR",
			InstrumentVersion: 1,
			ActiveTradeCount:  15,
		}
		target := &InstrumentInUseError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestValidationError(t *testing.T) {
	t.Run("Error with cause returns formatted message", func(t *testing.T) {
		err := &ValidationError{
			Operation: "settlement_lock_check",
			Message:   "failed to query positions",
			Cause:     errServiceUnavailable,
		}

		msg := err.Error()
		assert.Contains(t, msg, "settlement_lock_check")
		assert.Contains(t, msg, "failed to query positions")
		assert.Contains(t, msg, "service unavailable")
	})

	t.Run("Error without cause returns formatted message", func(t *testing.T) {
		err := &ValidationError{
			Operation: "deprecation",
			Message:   "invalid state",
		}

		msg := err.Error()
		assert.Contains(t, msg, "deprecation")
		assert.Contains(t, msg, "invalid state")
		assert.NotContains(t, msg, "nil")
	})

	t.Run("Unwrap returns underlying cause", func(t *testing.T) {
		err := &ValidationError{
			Operation: "test",
			Message:   "test message",
			Cause:     errCloseFailed,
		}

		unwrapped := err.Unwrap()
		assert.Equal(t, errCloseFailed, unwrapped)
	})

	t.Run("errors.Is matches wrapped error", func(t *testing.T) {
		err := &ValidationError{
			Operation: "test",
			Message:   "test message",
			Cause:     errServiceUnavailable,
		}

		assert.True(t, errors.Is(err, errServiceUnavailable))
	})

	t.Run("Is matches ValidationError", func(t *testing.T) {
		err := &ValidationError{Operation: "test", Message: "test"}
		target := &ValidationError{}

		assert.True(t, err.Is(target))
		assert.True(t, errors.Is(err, target))
	})
}

func TestErrorsAsChain(t *testing.T) {
	t.Run("errors.As extracts SettlementLockError", func(t *testing.T) {
		original := &SettlementLockError{
			InstrumentCode:    "USD",
			InstrumentVersion: 1,
			PositionCount:     3,
			SettlementIDs:     []string{"s1"},
		}

		wrapped := &ValidationError{
			Operation: "rebucketing",
			Message:   "settlement lock check failed",
			Cause:     original,
		}

		var lockErr *SettlementLockError
		require.True(t, errors.As(wrapped, &lockErr))
		assert.Equal(t, "USD", lockErr.InstrumentCode)
		assert.Equal(t, 1, lockErr.InstrumentVersion)
		assert.Equal(t, 3, lockErr.PositionCount)
	})

	t.Run("errors.As extracts InstrumentNotFoundError", func(t *testing.T) {
		original := &InstrumentNotFoundError{
			InstrumentCode:    "UNKNOWN",
			InstrumentVersion: 99,
		}

		wrapped := &ValidationError{
			Operation: "deprecation",
			Message:   "instrument lookup failed",
			Cause:     original,
		}

		var notFoundErr *InstrumentNotFoundError
		require.True(t, errors.As(wrapped, &notFoundErr))
		assert.Equal(t, "UNKNOWN", notFoundErr.InstrumentCode)
		assert.Equal(t, 99, notFoundErr.InstrumentVersion)
	})
}
