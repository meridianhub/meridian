package service

import (
	"errors"
	"testing"

	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerRegistry_ClosedState(t *testing.T) {
	registry := NewCircuitBreakerRegistry(nil)

	// Initial state should be closed
	assert.Equal(t, gobreaker.StateClosed, registry.State(ServicePositionKeeping))

	// Successful call should pass through
	result, err := registry.Execute(ServicePositionKeeping, func() (any, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestCircuitBreakerRegistry_TripsAfterThreshold(t *testing.T) {
	registry := NewCircuitBreakerRegistry(nil)

	simulatedErr := errors.New("upstream failure")

	// Fail 5 times (the threshold) to trip the circuit
	for i := 0; i < circuitBreakerFailureThreshold; i++ {
		_, err := registry.Execute(ServicePositionKeeping, func() (any, error) {
			return nil, simulatedErr
		})
		require.Error(t, err)
	}

	// Circuit should now be open
	assert.Equal(t, gobreaker.StateOpen, registry.State(ServicePositionKeeping))

	// Next call should fail fast with circuit breaker error
	_, err := registry.Execute(ServicePositionKeeping, func() (any, error) {
		return "should not reach", nil
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, gobreaker.ErrOpenState))
}

func TestCircuitBreakerRegistry_UnknownService(t *testing.T) {
	registry := NewCircuitBreakerRegistry(nil)

	// Unknown service should execute directly without circuit breaker
	result, err := registry.Execute("unknown-service", func() (any, error) {
		return "direct", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "direct", result)

	// State of unknown service defaults to closed
	assert.Equal(t, gobreaker.StateClosed, registry.State("unknown-service"))
}

func TestCircuitBreakerRegistry_AllServices(t *testing.T) {
	registry := NewCircuitBreakerRegistry(nil)

	services := []string{
		ServicePositionKeeping,
		ServiceFinancialAccounting,
		ServiceCurrentAccount,
	}

	for _, svc := range services {
		t.Run(svc, func(t *testing.T) {
			// Each service should have its own circuit breaker
			assert.Equal(t, gobreaker.StateClosed, registry.State(svc))

			result, err := registry.Execute(svc, func() (any, error) {
				return svc, nil
			})
			require.NoError(t, err)
			assert.Equal(t, svc, result)
		})
	}
}

func TestCircuitBreakerRegistry_IndependentBreakers(t *testing.T) {
	registry := NewCircuitBreakerRegistry(nil)

	simulatedErr := errors.New("upstream failure")

	// Trip PK circuit breaker
	for i := 0; i < circuitBreakerFailureThreshold; i++ {
		registry.Execute(ServicePositionKeeping, func() (any, error) {
			return nil, simulatedErr
		})
	}

	// PK should be open
	assert.Equal(t, gobreaker.StateOpen, registry.State(ServicePositionKeeping))

	// FA should still be closed (independent)
	assert.Equal(t, gobreaker.StateClosed, registry.State(ServiceFinancialAccounting))

	// FA calls should still work
	result, err := registry.Execute(ServiceFinancialAccounting, func() (any, error) {
		return "still-working", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "still-working", result)
}

func TestStateToInt(t *testing.T) {
	tests := []struct {
		state    gobreaker.State
		expected int
	}{
		{gobreaker.StateClosed, 0},
		{gobreaker.StateHalfOpen, 1},
		{gobreaker.StateOpen, 2},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			assert.Equal(t, tt.expected, stateToInt(tt.state))
		})
	}
}
