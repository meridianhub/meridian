// Package dispatch provides shared dispatch infrastructure for gateway services.
// It contains circuit breaker state machines, retry policies with exponential backoff,
// dispatcher interfaces, and generic poll-dispatch worker patterns that can be reused
// across both operational and inbound gateways.
package dispatch

import "time"

// CircuitState represents the current state of a circuit breaker.
type CircuitState string

const (
	// CircuitStateClosed means the circuit is closed and requests flow normally.
	CircuitStateClosed CircuitState = "CLOSED"
	// CircuitStateOpen means the circuit is open and requests are blocked.
	CircuitStateOpen CircuitState = "OPEN"
	// CircuitStateHalfOpen means the circuit is allowing a probe request to test recovery.
	CircuitStateHalfOpen CircuitState = "HALF_OPEN"
)

// HealthStatus represents the observed health of a connection endpoint.
type HealthStatus string

const (
	// HealthStatusUnknown means no health check has been performed yet.
	HealthStatusUnknown HealthStatus = "UNKNOWN"
	// HealthStatusHealthy means the endpoint is responding normally.
	HealthStatusHealthy HealthStatus = "HEALTHY"
	// HealthStatusDegraded means the endpoint is responding but with elevated latency or errors.
	HealthStatusDegraded HealthStatus = "DEGRADED"
	// HealthStatusUnhealthy means the endpoint is not responding or returning errors.
	HealthStatusUnhealthy HealthStatus = "UNHEALTHY"
)

// RetryPolicy defines how failed dispatch attempts should be retried.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of dispatch attempts (including the initial attempt).
	MaxAttempts int
	// InitialBackoff is the wait duration before the first retry.
	InitialBackoff time.Duration
	// MaxBackoff is the maximum wait duration between retries.
	MaxBackoff time.Duration
	// BackoffMultiplier is the dimensionless scaling factor applied to the backoff duration
	// on each retry (e.g., 2.0 doubles the backoff). This is a pure numeric multiplier,
	// not a duration.
	BackoffMultiplier float64
}
