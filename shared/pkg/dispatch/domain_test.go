package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitStateConstants(t *testing.T) {
	assert.Equal(t, CircuitState("CLOSED"), CircuitStateClosed)
	assert.Equal(t, CircuitState("OPEN"), CircuitStateOpen)
	assert.Equal(t, CircuitState("HALF_OPEN"), CircuitStateHalfOpen)
}

func TestHealthStatusConstants(t *testing.T) {
	assert.Equal(t, HealthStatus("UNKNOWN"), HealthStatusUnknown)
	assert.Equal(t, HealthStatus("HEALTHY"), HealthStatusHealthy)
	assert.Equal(t, HealthStatus("DEGRADED"), HealthStatusDegraded)
	assert.Equal(t, HealthStatus("UNHEALTHY"), HealthStatusUnhealthy)
}

func TestRetryPolicy(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
	}
	assert.Equal(t, 3, policy.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, policy.InitialBackoff)
	assert.Equal(t, 10*time.Second, policy.MaxBackoff)
	assert.Equal(t, 2.0, policy.BackoffMultiplier)
}
