package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_NewStartsClosed(t *testing.T) {
	cb := NewCircuitBreaker()
	assert.Equal(t, CircuitStateClosed, cb.State())
	assert.True(t, cb.IsAvailable())
	assert.Equal(t, 0, cb.FailureCount())
	assert.Equal(t, 0, cb.SuccessCount())
	assert.Nil(t, cb.OpenedAt())
}

func TestCircuitBreaker_RecordSuccess_ResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker()
	threshold := 5

	for i := 0; i < threshold-1; i++ {
		require.NoError(t, cb.RecordFailure(threshold))
	}
	assert.Equal(t, threshold-1, cb.FailureCount())
	assert.Equal(t, CircuitStateClosed, cb.State())

	cb.RecordSuccess()
	assert.Equal(t, 0, cb.FailureCount())
	assert.Equal(t, 1, cb.SuccessCount())
	assert.Equal(t, CircuitStateClosed, cb.State())
}

func TestCircuitBreaker_RecordFailureBelowThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	threshold := 5

	for i := 1; i < threshold; i++ {
		require.NoError(t, cb.RecordFailure(threshold))
		assert.Equal(t, i, cb.FailureCount())
		assert.Equal(t, CircuitStateClosed, cb.State())
	}
}

func TestCircuitBreaker_RecordFailureAtThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	threshold := 5

	for i := 0; i < threshold; i++ {
		require.NoError(t, cb.RecordFailure(threshold))
	}

	assert.Equal(t, threshold, cb.FailureCount())
	assert.Equal(t, CircuitStateOpen, cb.State())
	assert.NotNil(t, cb.OpenedAt())
}

func TestCircuitBreaker_RecordFailureInvalidThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	assert.ErrorIs(t, cb.RecordFailure(0), ErrInvalidThreshold)
	assert.ErrorIs(t, cb.RecordFailure(-1), ErrInvalidThreshold)
}

func TestCircuitBreaker_IsAvailableWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker()
	assert.True(t, cb.IsAvailable())
}

func TestCircuitBreaker_IsAvailableWhenOpen(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.TripCircuit()
	assert.False(t, cb.IsAvailable())
}

func TestCircuitBreaker_IsAvailableWhenHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.TripCircuit()
	cb.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, cb.State())
	assert.True(t, cb.IsAvailable())
}

func TestCircuitBreaker_TripCircuit(t *testing.T) {
	cb := NewCircuitBreaker()
	assert.Nil(t, cb.OpenedAt())

	cb.TripCircuit()

	assert.Equal(t, CircuitStateOpen, cb.State())
	require.NotNil(t, cb.OpenedAt())
	assert.WithinDuration(t, time.Now(), *cb.OpenedAt(), time.Second)
}

func TestCircuitBreaker_TripCircuit_PreservesOpenedAt(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.TripCircuit()
	original := *cb.OpenedAt()

	cb.TripCircuit()
	assert.Equal(t, original, *cb.OpenedAt())
}

func TestCircuitBreaker_AttemptReset(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.TripCircuit()
	assert.Equal(t, CircuitStateOpen, cb.State())

	cb.AttemptReset()

	assert.Equal(t, CircuitStateHalfOpen, cb.State())
}

func TestCircuitBreaker_AttemptReset_NoopWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.AttemptReset()
	assert.Equal(t, CircuitStateClosed, cb.State())
}

func TestCircuitBreaker_FullCycle(t *testing.T) {
	cb := NewCircuitBreaker()
	threshold := 3

	// Trip the circuit
	for i := 0; i < threshold; i++ {
		require.NoError(t, cb.RecordFailure(threshold))
	}
	assert.Equal(t, CircuitStateOpen, cb.State())
	assert.False(t, cb.IsAvailable())

	// Move to half-open
	cb.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, cb.State())
	assert.True(t, cb.IsAvailable())

	// Successful probe closes circuit
	cb.RecordSuccess()
	assert.Equal(t, CircuitStateClosed, cb.State())
	assert.Equal(t, 0, cb.FailureCount())
	assert.Nil(t, cb.OpenedAt())
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker()
	threshold := 3

	for i := 0; i < threshold; i++ {
		require.NoError(t, cb.RecordFailure(threshold))
	}
	cb.AttemptReset()
	assert.Equal(t, CircuitStateHalfOpen, cb.State())

	require.NoError(t, cb.RecordFailure(threshold))
	assert.Equal(t, CircuitStateOpen, cb.State())
	assert.NotNil(t, cb.OpenedAt())
}

func TestCircuitBreaker_LastUpdated(t *testing.T) {
	cb := NewCircuitBreaker()
	before := time.Now()

	cb.RecordSuccess()

	assert.True(t, !cb.LastUpdated().Before(before))
	assert.True(t, !cb.LastUpdated().After(time.Now()))
}
